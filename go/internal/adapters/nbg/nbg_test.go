package nbg

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
	a := New(vcrtest.Client(t, "nbg", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 2), adapter.Date(2026, 3, 4))
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

func TestFetchDateRange(t *testing.T) {
	if len(fetch(t)) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t)
	n := 0
	for _, r := range rates {
		if r.Date.Equal(rates[0].Date) {
			n++
		}
	}
	if n <= 1 {
		t.Errorf("got %d rates for %v, want more than 1", n, rates[0].Date)
	}
}

func TestParseBaseAndQuote(t *testing.T) {
	rates := mustParse(t, `[{"date": "2026-03-02T00:00:00", "currencies": [
  {"code": "USD", "quantity": 1, "rate": 2.7345, "name": "US Dollar",
   "diff": 0.001, "date": "2026-03-02T00:00:00", "validFromDate": "2026-03-03T00:00:00"}
]}]`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "USD" || r.Quote != "GEL" || math.Abs(r.Rate-2.7345) > 0.0001 {
		t.Errorf("rate = %+v", r)
	}
}

func TestParseNormalizesByQuantity(t *testing.T) {
	rates := mustParse(t, `[{"date": "2026-03-02T00:00:00", "currencies": [
  {"code": "JPY", "quantity": 100, "rate": 1.8200, "name": "Japanese Yen",
   "diff": 0.001, "date": "2026-03-02T00:00:00", "validFromDate": "2026-03-03T00:00:00"}
]}]`)
	if len(rates) == 0 || math.Abs(rates[0].Rate-0.0182) > 0.0001 {
		t.Errorf("rates = %+v, want 0.0182", rates)
	}
}

func TestParseSkips(t *testing.T) {
	tests := []struct{ name, json string }{
		{"zero rates", `[{"date": "2026-03-02T00:00:00", "currencies": [
  {"code": "USD", "quantity": 1, "rate": 0.0, "name": "US Dollar",
   "diff": 0.0, "date": "2026-03-02T00:00:00", "validFromDate": "2026-03-03T00:00:00"}
]}]`},
		{"invalid currency codes", `[{"date": "2026-03-02T00:00:00", "currencies": [
  {"code": "XX", "quantity": 1, "rate": 1.5, "name": "Invalid",
   "diff": 0.0, "date": "2026-03-02T00:00:00", "validFromDate": "2026-03-03T00:00:00"}
]}]`},
		{"empty response", `[]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rates := mustParse(t, tt.json); len(rates) != 0 {
				t.Errorf("got %d rates, want none", len(rates))
			}
		})
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 3, 2), adapter.Date(2026, 3, 4))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
