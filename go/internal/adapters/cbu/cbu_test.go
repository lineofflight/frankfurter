package cbu

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
	a := New(vcrtest.Client(t, "cbu", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 4, 1), adapter.Date(2026, 4, 3))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func mustParse(t *testing.T, s string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(s))
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

func TestParse(t *testing.T) {
	rates := mustParse(t, `[
		{"id":69,"Code":"840","Ccy":"USD","CcyNm_EN":"US Dollar","Nominal":"1","Rate":"12194.21","Diff":"-16.5","Date":"01.04.2026"},
		{"id":21,"Code":"978","Ccy":"EUR","CcyNm_EN":"Euro","Nominal":"1","Rate":"13984.32","Diff":"-51.89","Date":"01.04.2026"}
	]`)
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want 2", len(rates))
	}
	if rates[0].Base != "USD" || rates[0].Quote != "UZS" {
		t.Errorf("first rate = %+v, want USD/UZS", rates[0])
	}
	if math.Abs(rates[0].Rate-12194.21) > 0.01 {
		t.Errorf("rate = %v, want 12194.21", rates[0].Rate)
	}
}

func TestParseNormalizesByNominal(t *testing.T) {
	rates := mustParse(t, `[{"id":1,"Code":"360","Ccy":"IDR","CcyNm_EN":"Indonesian Rupiah","Nominal":"10","Rate":"7.18","Diff":"0","Date":"01.04.2026"}]`)
	if len(rates) != 1 || math.Abs(rates[0].Rate-0.718) > 0.001 {
		t.Errorf("got %+v, want one rate of 0.718", rates)
	}
}

func TestParseSkipsZeroRates(t *testing.T) {
	rates := mustParse(t, `[{"id":1,"Code":"840","Ccy":"USD","CcyNm_EN":"US Dollar","Nominal":"1","Rate":"0","Diff":"0","Date":"01.04.2026"}]`)
	if len(rates) != 0 {
		t.Errorf("got %+v, want none", rates)
	}
}

func TestParseRestoresOldRubleBeforeFirstNewRubleBulletin(t *testing.T) {
	rates := mustParse(t, `[
		{"id":1,"Code":"810","Ccy":"RUB","CcyNm_EN":"Russian Ruble","Nominal":"1000","Rate":"13.46","Diff":"0","Date":"30.12.1997"},
		{"id":1,"Code":"643","Ccy":"RUB","CcyNm_EN":"Russian Ruble","Nominal":"1","Rate":"13.48","Diff":"0","Date":"06.01.1998"}
	]`)
	if len(rates) != 2 || rates[0].Base != "RUR" || rates[1].Base != "RUB" {
		t.Fatalf("got %+v, want bases RUR, RUB", rates)
	}
	if math.Abs(rates[0].Rate-0.01346) > 1e-9 {
		t.Errorf("rate = %v, want 0.01346", rates[0].Rate)
	}
}

func TestParseSkipsInvalidCurrencyCodes(t *testing.T) {
	rates := mustParse(t, `[{"id":1,"Code":"999","Ccy":"XX","CcyNm_EN":"Invalid","Nominal":"1","Rate":"1.5","Diff":"0","Date":"01.04.2026"}]`)
	if len(rates) != 0 {
		t.Errorf("got %+v, want none", rates)
	}
}

func TestParseHandlesEmptyResponse(t *testing.T) {
	if rates := mustParse(t, "[]"); len(rates) != 0 {
		t.Errorf("got %+v, want none", rates)
	}
}

func TestParseRejectsNonArray(t *testing.T) {
	if _, err := parse([]byte(`{"error": true}`)); err == nil {
		t.Error("want an error for a JSON object")
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 4, 1), adapter.Date(2026, 4, 3))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
