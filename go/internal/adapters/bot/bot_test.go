package bot

import (
	"context"
	"io"
	"math"
	"net/http"
	"slices"
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
	a := New(vcrtest.Client(t, "bot", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 4, 1), adapter.Date(2026, 4, 3))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetch(t *testing.T) {
	if len(fetch(t)) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchReturnsMultipleCurrencies(t *testing.T) {
	var currencies []string
	for _, r := range fetch(t) {
		if !slices.Contains(currencies, r.Base) {
			currencies = append(currencies, r.Base)
		}
	}
	if len(currencies) <= 10 {
		t.Errorf("got %d currencies, want more than 10", len(currencies))
	}
	for _, c := range []string{"USD", "EUR"} {
		if !slices.Contains(currencies, c) {
			t.Errorf("missing %s", c)
		}
	}
}

func TestFetchQuotesInTHB(t *testing.T) {
	for _, r := range fetch(t) {
		if r.Quote != "THB" {
			t.Errorf("quote = %s, want THB", r.Quote)
		}
	}
}

func TestParseNormalisesPerUnitRates(t *testing.T) {
	records, err := parse([]byte(`{"result":{"data":{"data_detail":[
		{"period":"2026-04-03","currency_id":"JPY","currency_name_eng":"JAPAN : YEN (100 YEN) (JPY)","mid_rate":"20.4832000"},
		{"period":"2026-04-03","currency_id":"IDR","currency_name_eng":"INDONESIA : RUPIAH (1,000 RUPIAH) (IDR)","mid_rate":"1.9296000"},
		{"period":"2026-04-03","currency_id":"USD","currency_name_eng":"USA : DOLLAR (USD)","mid_rate":"32.6448000"}
	]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	rates := map[string]float64{}
	for _, r := range records {
		rates[r.Base] = r.Rate
	}
	for _, c := range []struct {
		base        string
		want, delta float64
	}{
		{"JPY", 0.204832, 0.0001},
		{"IDR", 0.0019296, 0.00001},
		{"USD", 32.6448, 0.01},
	} {
		got, ok := rates[c.base]
		if !ok || math.Abs(got-c.want) > c.delta {
			t.Errorf("%s = %v, want %v", c.base, got, c.want)
		}
	}
}

func TestGolden(t *testing.T) {
	vcrtest.SetSecrets(t)
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 4, 1), adapter.Date(2026, 4, 3))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchRequest(t *testing.T) {
	t.Setenv("BOT_API_KEY", "secret")
	var got *http.Request
	a := New(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r
		body := `{"result":{"data":{"data_detail":[]}}}`
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})})
	a.Now = func() time.Time { return time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC) }
	if _, err := a.Fetch(context.Background(), adapter.Date(2026, 4, 1), time.Time{}); err != nil {
		t.Fatal(err)
	}
	if got.URL.Host != "gateway.api.bot.or.th" || got.URL.Path != "/Stat-ExchangeRate/v2/DAILY_AVG_EXG_RATE/" {
		t.Errorf("url = %s", got.URL)
	}
	q := got.URL.Query()
	if q.Get("start_period") != "2026-04-01" || q.Get("end_period") != "2026-04-10" {
		t.Errorf("query = %s", got.URL.RawQuery)
	}
	if got.Header.Get("Authorization") != "secret" || got.Header.Get("Accept") != "application/json" {
		t.Errorf("headers = %v", got.Header)
	}
}

func TestFetchRequiresAPIKey(t *testing.T) {
	t.Setenv("BOT_API_KEY", "")
	if _, err := New(http.DefaultClient).Fetch(context.Background(), adapter.Date(2026, 4, 1), adapter.Date(2026, 4, 3)); err == nil {
		t.Fatal("want error")
	}
}

func TestParseSkipsBlankAndZeroRates(t *testing.T) {
	records, err := parse([]byte(`{"result":{"data":{"data_detail":[
		{"period":"2026-04-03","currency_id":"AAA","currency_name_eng":"A","mid_rate":null},
		{"period":"2026-04-03","currency_id":"BBB","currency_name_eng":"B","mid_rate":""},
		{"period":"2026-04-03","currency_id":"CCC","currency_name_eng":"C"},
		{"period":"2026-04-03","currency_id":"DDD","currency_name_eng":"D","mid_rate":"0.0000000"},
		{"period":"2026-04-03","currency_id":"EEE","currency_name_eng":"E (100 E)","mid_rate":25.5}
	]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Base != "EEE" || records[0].Rate != 0.255 ||
		!records[0].Date.Equal(adapter.Date(2026, 4, 3)) {
		t.Errorf("got %+v", records)
	}
}

func TestParseErrors(t *testing.T) {
	for _, body := range []string{
		`{"result":{"data":{}}}`,
		`{"result":{"data":{"data_detail":[{"period":"2026-04-03","currency_id":"USD","mid_rate":"n/a"}]}}}`,
	} {
		if _, err := parse([]byte(body)); err == nil {
			t.Errorf("no error for %s", body)
		}
	}
}
