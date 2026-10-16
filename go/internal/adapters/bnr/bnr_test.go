package bnr

import (
	"context"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T, after, upto time.Time) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "bnr", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func dates(rates []adapter.Rate) []time.Time {
	var ds []time.Time
	for _, r := range rates {
		if !slices.ContainsFunc(ds, r.Date.Equal) {
			ds = append(ds, r.Date)
		}
	}
	slices.SortFunc(ds, time.Time.Compare)
	return ds
}

func find(rates []adapter.Rate, base string) adapter.Rate {
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Base == base })
	if i < 0 {
		return adapter.Rate{}
	}
	return rates[i]
}

func TestFetchWithDateRange(t *testing.T) {
	if rates := fetch(t, adapter.Date(2026, 4, 1), adapter.Date(2026, 4, 5)); len(rates) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 4, 1), adapter.Date(2026, 4, 5))
	first := dates(rates)[0]
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

func TestParse(t *testing.T) {
	rates, err := parse([]byte(`<?xml version="1.0" encoding="utf-8"?>
<DataSet xmlns="http://www.bnr.ro/xsd">
  <Header><Publisher>National Bank of Romania</Publisher></Header>
  <Body>
    <Cube date="2026-04-03">
      <Rate currency="EUR">5.0978</Rate>
      <Rate currency="USD">4.4169</Rate>
      <Rate currency="HUF" multiplier="100">1.3229</Rate>
    </Cube>
  </Body>
</DataSet>`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 3 {
		t.Fatalf("got %d rates, want 3", len(rates))
	}
	eur := find(rates, "EUR")
	if eur.Quote != "RON" || eur.Rate != 5.0978 || !eur.Date.Equal(adapter.Date(2026, 4, 3)) {
		t.Errorf("EUR = %+v", eur)
	}
	if usd := find(rates, "USD"); usd.Rate != 4.4169 {
		t.Errorf("USD = %v, want 4.4169", usd.Rate)
	}
	if huf := find(rates, "HUF"); math.Abs(huf.Rate-0.013229) > 0.000001 {
		t.Errorf("HUF = %v, want 0.013229", huf.Rate)
	}
}

func TestFetchFiltersByDateRange(t *testing.T) {
	ds := dates(fetch(t, adapter.Date(2026, 4, 1), adapter.Date(2026, 4, 3)))
	for _, want := range []time.Time{adapter.Date(2026, 4, 2), adapter.Date(2026, 4, 3)} {
		if !slices.ContainsFunc(ds, want.Equal) {
			t.Errorf("dates %v lack %v", ds, want)
		}
	}
	for _, d := range ds {
		if !d.After(adapter.Date(2026, 4, 1)) {
			t.Errorf("date %v not after April 1", d)
		}
	}
}

func TestParseNormalizesGoldToTroyOunces(t *testing.T) {
	rates, err := parse([]byte(`<?xml version="1.0" encoding="utf-8"?>
<DataSet xmlns="http://www.bnr.ro/xsd">
  <Header><Publisher>BNR</Publisher></Header>
  <Body>
    <Cube date="2026-04-24">
      <Rate currency="XAU">656.3566</Rate>
    </Cube>
  </Body>
</DataSet>`))
	if err != nil {
		t.Fatal(err)
	}
	if xau := find(rates, "XAU"); math.Abs(xau.Rate-656.3566*adapter.GramsPerTroyOunce) > 0.0001 {
		t.Errorf("XAU = %v", xau.Rate)
	}
}

func TestFetchEarlierDatesFromYearlyArchive(t *testing.T) {
	ds := dates(fetch(t, adapter.Date(2026, 1, 4), adapter.Date(2026, 1, 10)))
	if len(ds) == 0 {
		t.Fatal("no rates")
	}
	if ds[0].After(adapter.Date(2026, 1, 10)) || ds[0].Before(adapter.Date(2026, 1, 5)) {
		t.Errorf("earliest date %v outside January 5 to 10", ds[0])
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
