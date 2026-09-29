package nrb

import (
	"context"
	"math"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "nrb", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 4, 1), adapter.Date(2026, 4, 5))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func find(rates []adapter.Rate, base string) adapter.Rate {
	for _, r := range rates {
		if r.Base == base {
			return r
		}
	}
	return adapter.Rate{}
}

func TestFetchWithDateRange(t *testing.T) {
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
	if n != 22 {
		t.Errorf("got %d rates on %s, want 22", n, first.Format("2006-01-02"))
	}
}

func TestParseRatesFromPayload(t *testing.T) {
	rates, err := parse([]byte(`[{"date": "2026-04-01", "rates": [
		{"currency": {"iso3": "USD", "name": "U.S. Dollar", "unit": 1}, "buy": "151.44", "sell": "152.04"},
		{"currency": {"iso3": "EUR", "name": "European Euro", "unit": 1}, "buy": "173.69", "sell": "174.38"}
	]}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want 2", len(rates))
	}
	r := rates[0]
	if r.Base != "USD" || r.Quote != "NPR" || !r.Date.Equal(adapter.Date(2026, 4, 1)) {
		t.Errorf("first rate = %+v", r)
	}
	if math.Abs(r.Rate-151.74) > 0.01 {
		t.Errorf("rate = %v, want 151.74", r.Rate)
	}
}

func TestParseSkipsMalformedDates(t *testing.T) {
	rates, err := parse([]byte(`[{"date": "2020-06.25",
		"rates": [{"currency": {"iso3": "USD", "unit": 1}, "buy": "133.5", "sell": "134.0"}]}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseNormalizesRatesByUnit(t *testing.T) {
	rates, err := parse([]byte(`[{"date": "2026-04-01", "rates": [
		{"currency": {"iso3": "JPY", "name": "Japanese Yen", "unit": 10}, "buy": "9.49", "sell": "9.53"},
		{"currency": {"iso3": "KRW", "name": "South Korean Won", "unit": 100}, "buy": "9.92", "sell": "9.96"}
	]}]`))
	if err != nil {
		t.Fatal(err)
	}
	// JPY: (9.49 + 9.53) / 2.0 / 10 = 0.951
	if got := find(rates, "JPY").Rate; math.Abs(got-0.951) > 0.001 {
		t.Errorf("JPY rate = %v, want 0.951", got)
	}
	// KRW: (9.92 + 9.96) / 2.0 / 100 = 0.0994
	if got := find(rates, "KRW").Rate; math.Abs(got-0.0994) > 0.0001 {
		t.Errorf("KRW rate = %v, want 0.0994", got)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 4, 1), adapter.Date(2026, 4, 5))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
