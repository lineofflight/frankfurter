package hnb

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
	a := New(vcrtest.Client(t, "hnb", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host, vcrtest.Path)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2023, 1, 2), adapter.Date(2023, 1, 6))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchRates(t *testing.T) {
	rates := fetch(t)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	if rates[0].Base != "EUR" {
		t.Errorf("base = %q, want EUR", rates[0].Base)
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
		t.Errorf("got %d rates on %v, want several", n, first)
	}
}

func TestParseCommaDecimals(t *testing.T) {
	rates, err := parse([]byte(`[{"datum_primjene":"2023-01-02","valuta":"USD","srednji_tecaj":"1,066200"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "EUR" || r.Quote != "USD" || !r.Date.Equal(adapter.Date(2023, 1, 2)) {
		t.Errorf("rate = %+v", r)
	}
	if math.Abs(r.Rate-1.0662) > 0.0001 {
		t.Errorf("rate = %v, want 1.0662", r.Rate)
	}
}

func TestParseSkipsZeroRates(t *testing.T) {
	rates, err := parse([]byte(`[{"datum_primjene":"2023-01-02","valuta":"USD","srednji_tecaj":"0,000000"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2023, 1, 2), adapter.Date(2023, 1, 6))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
