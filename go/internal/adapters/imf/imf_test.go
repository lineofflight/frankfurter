package imf

import (
	"context"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetchMarch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "imf", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 31))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func uniqueDates(rates []adapter.Rate) []time.Time {
	var dates []time.Time
	for _, r := range rates {
		if !slices.ContainsFunc(dates, r.Date.Equal) {
			dates = append(dates, r.Date)
		}
	}
	return dates
}

type key struct {
	date        time.Time
	base, quote string
}

func assertNoDuplicates(t *testing.T, rates []adapter.Rate) {
	t.Helper()
	seen := map[key]bool{}
	for _, r := range rates {
		k := key{r.Date, r.Base, r.Quote}
		if seen[k] {
			t.Errorf("duplicate record %+v", k)
		}
		seen[k] = true
	}
}

func TestFetchAcrossBothCurrencyBlocks(t *testing.T) {
	dates := uniqueDates(fetchMarch(t))
	if !slices.ContainsFunc(dates, adapter.Date(2026, 3, 17).Equal) {
		t.Error("missing 2026-03-17")
	}
	if len(dates) <= 11 {
		t.Errorf("got %d dates, want more than 11", len(dates))
	}
}

func TestFetchMultipleCurrenciesPerDateWithoutDuplicates(t *testing.T) {
	rates := fetchMarch(t)
	first := uniqueDates(rates)[0]
	n := 0
	for _, r := range rates {
		if r.Date.Equal(first) {
			n++
		}
	}
	if n <= 1 {
		t.Errorf("got %d records on %s, want more than 1", n, first)
	}
	assertNoDuplicates(t, rates)
}

func mustParse(t *testing.T, fn func(string) ([]adapter.Rate, error), tsv string) []adapter.Rate {
	t.Helper()
	rates, err := fn(tsv)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestParseIndirectQuotes(t *testing.T) {
	rates := mustParse(t, parse, "Representative Exchange Rates for Selected Currencies for January 2026\n"+
		"Currency\tJanuary 02, 2026\tJanuary 05, 2026\n"+
		"Euro(1)\t1.1698\t1.1606\n"+
		"Japanese yen\t156.40\t157.41\n"+
		"U.S. dollar\t1.0000\t1.0000\n")

	var eur, jpy []adapter.Rate
	for _, r := range rates {
		if r.Base == "EUR" || r.Quote == "EUR" {
			eur = append(eur, r)
		}
		if r.Base == "USD" && r.Quote == "JPY" {
			jpy = append(jpy, r)
		}
	}
	if eur[0].Base != "EUR" || eur[0].Quote != "USD" {
		t.Errorf("EUR = %+v, want EUR/USD", eur[0])
	}
	if jpy[0].Rate != 156.4 {
		t.Errorf("JPY rate = %v, want 156.4", jpy[0].Rate)
	}
}

func TestParseKWDAsDirectQuote(t *testing.T) {
	rates := mustParse(t, parse, "Representative Exchange Rates for Selected Currencies for March 2026\n"+
		"Currency\tMarch 02, 2026\n"+
		"Kuwaiti dinar\t0.306600\n")

	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Quote == "KWD" || r.Base == "KWD" })
	if i < 0 {
		t.Fatal("no KWD record")
	}
	kwd := rates[i]
	if kwd.Base != "USD" || kwd.Quote != "KWD" || kwd.Rate != 0.3066 {
		t.Errorf("KWD = %+v, want USD/KWD 0.3066", kwd)
	}
}

func TestParseSkipsUSDRows(t *testing.T) {
	rates := mustParse(t, parse, "Representative Exchange Rates for Selected Currencies for January 2026\n"+
		"Currency\tJanuary 02, 2026\n"+
		"U.S. dollar\t1.0000\n"+
		"Euro(1)\t1.1698\n")

	for _, r := range rates {
		if r.Base == "USD" && r.Quote == "USD" {
			t.Errorf("unexpected USD/USD record %+v", r)
		}
	}
}

func TestParseContinuedBlocks(t *testing.T) {
	rates := mustParse(t, parse, "Representative Exchange Rates for Selected Currencies for March 2026\n"+
		"Currency\tMarch 02, 2026\tMarch 03, 2026\n"+
		"Chinese yuan\t6.882900\t6.897100\n"+
		"U.S. dollar\t1.000000\t1.000000\n"+
		"\n"+
		"Representative Exchange Rates for Selected Currencies for March 2026 Continued\n"+
		"\n"+
		"Currency\tMarch 17, 2026\tMarch 18, 2026\n"+
		"Chinese yuan\t6.888300\t6.875600\n"+
		"U.S. dollar\t1.000000\t1.000000\n")

	var cny []adapter.Rate
	for _, r := range rates {
		if r.Quote == "CNY" {
			cny = append(cny, r)
		}
	}
	sort.Slice(cny, func(i, j int) bool { return cny[i].Date.Before(cny[j].Date) })

	want := []time.Time{adapter.Date(2026, 3, 2), adapter.Date(2026, 3, 3), adapter.Date(2026, 3, 17), adapter.Date(2026, 3, 18)}
	if len(cny) != len(want) {
		t.Fatalf("got %d CNY records, want %d", len(cny), len(want))
	}
	for i, r := range cny {
		if !r.Date.Equal(want[i]) {
			t.Errorf("CNY[%d] date = %s, want %s", i, r.Date, want[i])
		}
	}
	if cny[2].Rate != 6.8883 {
		t.Errorf("CNY on 2026-03-17 = %v, want 6.8883", cny[2].Rate)
	}
}

func TestParseNoDuplicatesAcrossBlocks(t *testing.T) {
	rates := mustParse(t, parse, "Currency\tMarch 02, 2026\tMarch 03, 2026\n"+
		"Chinese yuan\t6.882900\t6.897100\n"+
		"\n"+
		"Currency\tMarch 17, 2026\tMarch 18, 2026\n"+
		"Chinese yuan\t6.888300\t6.875600\n")

	assertNoDuplicates(t, rates)
}

func TestFetchSDROnlyAsUSDXDR(t *testing.T) {
	var sdr []adapter.Rate
	for _, r := range fetchMarch(t) {
		if r.Quote == "XDR" {
			sdr = append(sdr, r)
		}
	}
	if len(sdr) == 0 {
		t.Fatal("no XDR records")
	}
	for _, r := range sdr {
		if r.Base != "USD" {
			t.Errorf("XDR record with base %s", r.Base)
		}
	}
}

func TestParseSDRCVKeepsOnlyUSD(t *testing.T) {
	rates := mustParse(t, parseSDRCV, "SDRs per Currency unit for April 2026\n"+
		"Currency\tApril 01, 2026\tApril 02, 2026\n"+
		"Euro\t0.8507900000\t0.8483160000\n"+
		"Japanese yen\t0.0046154900\t0.0046392700\n"+
		"U.S. dollar\t0.7331240000\t0.7360660000\n")

	if len(rates) != 2 {
		t.Fatalf("got %d records, want 2", len(rates))
	}
	for _, r := range rates {
		if r.Base != "USD" {
			t.Errorf("base = %s, want USD", r.Base)
		}
	}
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Date.Equal(adapter.Date(2026, 4, 1)) })
	if i < 0 {
		t.Fatal("no record on 2026-04-01")
	}
	if rates[i].Quote != "XDR" || rates[i].Rate != 0.7331240 {
		t.Errorf("USD = %+v, want USD/XDR 0.733124", rates[i])
	}
}

func TestParseSDRCVSkipsNA(t *testing.T) {
	rates := mustParse(t, parseSDRCV, "SDRs per Currency unit for April 2026\n"+
		"Currency\tApril 01, 2026\tApril 02, 2026\n"+
		"U.S. dollar\tNA\t0.7360660000\n")

	if len(rates) != 1 {
		t.Fatalf("got %d records, want 1", len(rates))
	}
	if !rates[0].Date.Equal(adapter.Date(2026, 4, 2)) {
		t.Errorf("date = %s, want 2026-04-02", rates[0].Date)
	}
}

func TestParseEmptyResponse(t *testing.T) {
	if _, err := parse("  \n"); err == nil {
		t.Error("want an error for an empty response")
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 31))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
