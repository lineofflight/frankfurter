package bam

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
	a := New(vcrtest.Client(t, "bam", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host, vcrtest.Path)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 25), adapter.Date(2026, 3, 27))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func mustParse(t *testing.T, json string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(json))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchWithDateRange(t *testing.T) {
	if len(fetch(t)) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
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

func TestParseBaseAndQuote(t *testing.T) {
	rates := mustParse(t, `[{"date": "2026-03-25T12:30:00", "libDevise": "USD", "moyen": 9.3794, "uniteDevise": 1}]`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "USD" || r.Quote != "MAD" {
		t.Errorf("got %s/%s, want USD/MAD", r.Base, r.Quote)
	}
	if math.Abs(r.Rate-9.3794) > 0.0001 {
		t.Errorf("rate = %v, want 9.3794", r.Rate)
	}
	if !r.Date.Equal(adapter.Date(2026, 3, 25)) {
		t.Errorf("date = %v, want 2026-03-25", r.Date)
	}
}

func TestParseNormalizesByUniteDevise(t *testing.T) {
	rates := mustParse(t, `[{"date": "2026-03-25T12:30:00", "libDevise": "JPY", "moyen": 6.2500, "uniteDevise": 100}]`)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	if math.Abs(rates[0].Rate-0.0625) > 0.0001 {
		t.Errorf("rate = %v, want 0.0625", rates[0].Rate)
	}
}

func TestParseRelabelsNewOuguiyaAndReadsUnitOneAsPerHundred(t *testing.T) {
	rates := mustParse(t, `[
		{"date": "2018-01-02T14:00:00", "libDevise": "MRO", "achat": 2.6161, "vente": 2.6318, "uniteDevise": 100},
		{"date": "2018-04-16T12:30:00", "libDevise": "MRO", "moyen": 25.864, "uniteDevise": 100},
		{"date": "2018-04-17T12:30:00", "libDevise": "MRO", "moyen": 25.857, "uniteDevise": 1}
	]`)
	var got []string
	var mids []*float64
	for _, r := range rates {
		got = append(got, r.Date.Format(time.DateOnly)+" "+r.Base)
		mids = append(mids, r.Mid)
	}
	if want := []string{"2018-01-02 MRO", "2018-04-16 MRU", "2018-04-17 MRU"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if mids[0] != nil || mids[1] == nil || *mids[1] != 0.25864 || mids[2] == nil || *mids[2] != 0.25857 {
		t.Errorf("mids = %v, want [nil 0.25864 0.25857]", mids)
	}
	if math.Abs(rates[0].Rate-0.0262395) > 1e-9 {
		t.Errorf("rate = %v, want 0.0262395", rates[0].Rate)
	}
}

func TestParseSkips(t *testing.T) {
	tests := map[string]string{
		"zero rates":             `[{"date": "2026-03-25T12:30:00", "libDevise": "USD", "moyen": 0.0, "uniteDevise": 1}]`,
		"invalid currency codes": `[{"date": "2026-03-25T12:30:00", "libDevise": "XY", "moyen": 9.3794, "uniteDevise": 1}]`,
		"empty response":         `[]`,
	}
	for name, json := range tests {
		t.Run(name, func(t *testing.T) {
			if rates := mustParse(t, json); len(rates) != 0 {
				t.Errorf("got %d rates, want none", len(rates))
			}
		})
	}
}

func TestParseFallsBackToBuySellAverage(t *testing.T) {
	rates := mustParse(t, `[{"date": "1999-01-04T14:00:00", "libDevise": "USD", "achat": 9.2125, "vente": 9.2679, "uniteDevise": 1}]`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	if math.Abs(rates[0].Rate-9.2402) > 0.0001 {
		t.Errorf("rate = %v, want 9.2402", rates[0].Rate)
	}
}

func TestParseRejectsNonArray(t *testing.T) {
	if _, err := parse([]byte(`{"error": true}`)); err == nil {
		t.Error("want an error for a JSON object")
	}
}

func TestGolden(t *testing.T) {
	vcrtest.SetSecrets(t)
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 3, 25), adapter.Date(2026, 3, 27))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

func TestParseScalesPriceComponents(t *testing.T) {
	rates := mustParse(t, `[{"date": "1999-01-04T14:00:00", "libDevise": "JPY", "achat": 7.4492, "vente": 7.5508, "uniteDevise": 100}]`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Mid != nil {
		t.Errorf("mid = %v, want nil when moyen is absent", *r.Mid)
	}
	if r.Bid == nil || *r.Bid != 0.074492 || r.Ask == nil || *r.Ask != 0.075508 {
		t.Errorf("bid/ask = %v/%v, want 0.074492/0.075508", r.Bid, r.Ask)
	}
	if math.Abs(r.Rate-0.075) > 1e-12 {
		t.Errorf("rate = %v, want 0.075", r.Rate)
	}
}

type recorder struct{ reqs []*http.Request }

func (rt *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.reqs = append(rt.reqs, req)
	body := io.NopCloser(strings.NewReader("[]"))
	return &http.Response{StatusCode: http.StatusOK, Body: body, Header: http.Header{}, Request: req}, nil
}

func TestFetchRequestsWeekdaysWithKey(t *testing.T) {
	t.Setenv("BAM_API_KEY", "secret")
	rt := &recorder{}
	a := New(&http.Client{Transport: rt})
	// Friday 2026-03-27 through Monday 2026-03-30.
	if _, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 27), adapter.Date(2026, 3, 30)); err != nil {
		t.Fatal(err)
	}
	var dates []string
	for _, req := range rt.reqs {
		if got := req.Header.Get("Ocp-Apim-Subscription-Key"); got != "secret" {
			t.Errorf("key header = %q", got)
		}
		if got := req.URL.Host + req.URL.Path; got != "api.centralbankofmorocco.ma/cours/Version1/api/CoursVirement" {
			t.Errorf("url = %s", req.URL)
		}
		dates = append(dates, req.URL.Query().Get("date"))
	}
	want := []string{"2026-03-27T12:30:00", "2026-03-30T12:30:00"}
	if !slices.Equal(dates, want) {
		t.Errorf("dates = %v, want %v", dates, want)
	}
}

func TestFetchNeedsAPIKey(t *testing.T) {
	t.Setenv("BAM_API_KEY", "")
	a := New(&http.Client{Transport: &recorder{}})
	if _, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 27), adapter.Date(2026, 3, 27)); err == nil {
		t.Error("want an error without an API key")
	}
}
