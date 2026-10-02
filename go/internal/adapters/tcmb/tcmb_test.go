package tcmb

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	vcrtest.SetSecrets(t)
	a := New(vcrtest.Client(t, "tcmb", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 22))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchSinceDate(t *testing.T) {
	if len(fetch(t)) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t)
	first := rates[0].Date
	n := 0
	for _, r := range rates {
		if r.Date.Equal(first) {
			n++
		}
	}
	if n <= 1 {
		t.Errorf("got %d rates on %s, want more than 1", n, first.Format("2006-01-02"))
	}
}

func TestFetchKeepsEveryDigitOfMid(t *testing.T) {
	// JPY is quoted per 100 units, so its rate lands three orders of magnitude
	// below the rest of the feed. Rounding to a fixed number of decimal places
	// clipped it: buy 28.0072 and sell 28.1927 average to 28.09995, which is
	// 0.2809995 per yen, and a round(4) stored 0.281.
	for _, r := range fetch(t) {
		if r.Base == "JPY" && r.Date.Equal(adapter.Date(2026, 3, 2)) {
			if r.Rate != 0.2809995 {
				t.Errorf("JPY rate = %v, want 0.2809995", r.Rate)
			}
			return
		}
	}
	t.Fatal("no JPY rate on 2026-03-02")
}

// Recorded from the repository root with:
//
//	APP_ENV=test mise exec -- bundle exec ruby go/scripts/golden.rb tcmb tcmb method,host \
//	  'fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 22))' \
//	  > go/internal/adapters/tcmb/testdata/golden/fetch.json
func TestGolden(t *testing.T) {
	vcrtest.SetSecrets(t)
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 22))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchRequest(t *testing.T) {
	t.Setenv("TCMB_API_KEY", "secret")
	var got *http.Request
	a := New(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		got = r
		body := io.NopCloser(strings.NewReader(`{"items":[]}`))
		return &http.Response{StatusCode: 200, Body: body, Header: http.Header{}}, nil
	})})
	a.Now = func() time.Time { return time.Date(2026, 3, 22, 12, 0, 0, 0, time.UTC) }
	if _, err := a.Fetch(context.Background(), time.Time{}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	u := got.URL.String()
	for _, want := range []string{
		"https://evds3.tcmb.gov.tr/igmevdsms-dis/series=TP.DK.AED.A.YTL-TP.DK.AED.S.YTL-TP.DK.AUD.A.YTL-",
		"-TP.DK.USD.A.YTL-TP.DK.USD.S.YTL&startDate=02-01-2012&endDate=22-03-2026&type=json&frequency=1",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("URL %s lacks %s", u, want)
		}
	}
	if k := got.Header.Get("key"); k != "secret" {
		t.Errorf("key header = %q", k)
	}
}

func TestFetchNoKey(t *testing.T) {
	t.Setenv("TCMB_API_KEY", "")
	if _, err := New(http.DefaultClient).Fetch(context.Background(), time.Time{}, time.Time{}); err == nil {
		t.Fatal("want error without API key")
	}
}

func TestParse(t *testing.T) {
	items := []map[string]any{{
		"Tarih":           "2-3-2026",
		"TP_DK_USD_A_YTL": "44.1",
		"TP_DK_USD_S_YTL": "44.3",
		"TP_DK_EUR_A_YTL": "50.0", // no sell: skipped
		"TP_DK_GBP_A_YTL": nil,
		"TP_DK_GBP_S_YTL": "58.0",
		"TP_DK_JPY_A_YTL": 28.0072,
		"TP_DK_JPY_S_YTL": "28.1927",
		"UNIXTIME":        "ignored",
	}}
	rates, err := parse(items)
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want JPY and USD: %+v", len(rates), rates)
	}
	jpy, usd := rates[0], rates[1]
	if !jpy.Date.Equal(adapter.Date(2026, 3, 2)) || jpy.Base != "JPY" || jpy.Quote != "TRY" ||
		jpy.Rate != 0.2809995 || *jpy.Bid != 0.280072 || *jpy.Ask != 0.281927 {
		t.Errorf("JPY = %+v bid %v ask %v", jpy, *jpy.Bid, *jpy.Ask)
	}
	if usd.Base != "USD" || usd.Rate != 44.2 || *usd.Bid != 44.1 || *usd.Ask != 44.3 {
		t.Errorf("USD = %+v", usd)
	}
}

func TestParseErrors(t *testing.T) {
	for name, item := range map[string]map[string]any{
		"bad value": {"Tarih": "02-03-2026", "TP_DK_USD_A_YTL": "n/a", "TP_DK_USD_S_YTL": "44.3"},
		"blank":     {"Tarih": "02-03-2026", "TP_DK_USD_A_YTL": "", "TP_DK_USD_S_YTL": "44.3"},
		"bad date":  {"Tarih": "2026-03-02"},
		"no date":   {},
	} {
		if _, err := parse([]map[string]any{item}); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
