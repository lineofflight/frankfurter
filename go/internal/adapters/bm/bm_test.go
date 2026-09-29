package bm

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
	a := New(vcrtest.Client(t, "bm", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func find(t *testing.T, rates []adapter.Rate, base string) adapter.Rate {
	t.Helper()
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Base == base })
	if i < 0 {
		t.Fatalf("no %s rate", base)
	}
	return rates[i]
}

func bases(rates []adapter.Rate) []string {
	var bs []string
	for _, r := range rates {
		bs = append(bs, r.Base)
	}
	return bs
}

func TestFetchMZNQuote(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 9, 2), adapter.Date(2026, 9, 4))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if r.Quote != "MZN" {
			t.Fatalf("quote %s, want MZN", r.Quote)
		}
	}
}

func TestFetchCoversAll19Currencies(t *testing.T) {
	got := bases(fetch(t, adapter.Date(2026, 9, 4), adapter.Date(2026, 9, 4)))
	slices.Sort(got)
	want := []string{
		"BRL", "BWP", "CAD", "CHF", "CNH", "CNY", "DKK", "EUR", "GBP", "JPY",
		"MUR", "MWK", "NOK", "SEK", "SZL", "TZS", "USD", "ZAR", "ZMW",
	}
	if !slices.Equal(got, want) {
		t.Errorf("bases = %v, want %v", got, want)
	}
}

func TestFetchEmitsPublishedMid(t *testing.T) {
	usd := find(t, fetch(t, adapter.Date(2026, 9, 4), adapter.Date(2026, 9, 4)), "USD")
	if usd.Rate != 63.91 {
		t.Errorf("USD = %v, want 63.91", usd.Rate)
	}
}

func TestFetchRescalesPer1000Block(t *testing.T) {
	jpy := find(t, fetch(t, adapter.Date(2026, 9, 4), adapter.Date(2026, 9, 4)), "JPY")
	if math.Abs(jpy.Rate-0.41013) > 1e-9 {
		t.Errorf("JPY = %v, want 0.41013", jpy.Rate)
	}
}

func TestFetchKeepsOnshoreAndOffshoreRenminbiApart(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 9, 4), adapter.Date(2026, 9, 4))
	if cny := find(t, rates, "CNY"); cny.Rate != 9.52 {
		t.Errorf("CNY = %v, want 9.52", cny.Rate)
	}
	if cnh := find(t, rates, "CNH"); cnh.Rate != 9.53 {
		t.Errorf("CNH = %v, want 9.53", cnh.Rate)
	}
}

func TestFetchFiltersByDateRange(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 9, 2), adapter.Date(2026, 9, 4))
	var got []time.Time
	for _, r := range rates {
		if !slices.ContainsFunc(got, r.Date.Equal) {
			got = append(got, r.Date)
		}
	}
	slices.SortFunc(got, time.Time.Compare)
	want := []time.Time{adapter.Date(2026, 9, 2), adapter.Date(2026, 9, 3), adapter.Date(2026, 9, 4)}
	if !slices.EqualFunc(got, want, time.Time.Equal) {
		t.Errorf("dates = %v, want %v", got, want)
	}
}

var parseDate = adapter.Date(2018, 1, 2)

// Bulletins before November 2020 carry buy and sell only.
const parseText = `                                        MERCADO CAMBIAL
                                      BOLETIM Nº  002/18
1. TAXAS DE CÂMBIO MÉDIAS DE REFERÊNCIA EM METICAIS DO DIA 02 Janeiro
   de 2018

                                               CÂMBIOS(MT)
PAÍSES                  MOEDAS           COMPRA          VENDA

Estados Unidos(a)       Dolar               58,40          59,56

2.   OUTRAS TAXAS MÉDIAS (b)
2.1. PAÍSES VIZINHOS
2.1.1 Meticais por Unidade de Moeda
       PAÍSES           MOEDAS
  Àfrica do Sul         Rand                 4,74            4,83
  Swazilândia           Lilangueni           4,74            4,83

2.1.2 Meticais por 1000 Unidades de Moeda

       PAÍSES           MOEDAS
  Japão                 Iene               519,79         530,09
  Zimbabwe              Dólar              154,50         157,56

2.2. OUTROS PAÍSES
2.2.1 Meticais por Unidade de Moeda
        PAÍSES          MOEDAS
  China/Offshore        Rememb               9,00            9,18
  China                 Rememb               9,00            9,17

3. OUTRAS INFORMAÇÕES

1. PRIME RATE - Nova Iorque.......................     4,5000000   %
3. OURO/-USD/Onça:
Compra.............   1.311,91000
Venda..............   1.312,68000
`

func parsed(t *testing.T) []adapter.Rate {
	t.Helper()
	rates, err := parse(parseText, parseDate)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestParseSynthesisesMidWithoutMidColumn(t *testing.T) {
	if usd := find(t, parsed(t), "USD"); usd.Rate != 58.98 {
		t.Errorf("USD = %v, want 58.98", usd.Rate)
	}
}

func TestParseRescalesPer1000AndResetsAtNextHeading(t *testing.T) {
	rates := parsed(t)
	if jpy := find(t, rates, "JPY"); math.Abs(jpy.Rate-0.52494) > 1e-9 {
		t.Errorf("JPY = %v, want 0.52494", jpy.Rate)
	}
	if cny := find(t, rates, "CNY"); cny.Rate != 9.085 {
		t.Errorf("CNY = %v, want 9.085", cny.Rate)
	}
}

func TestParseMapsFormerNameOfESwatini(t *testing.T) {
	if szl := find(t, parsed(t), "SZL"); szl.Rate != 4.785 {
		t.Errorf("SZL = %v, want 4.785", szl.Rate)
	}
}

func TestParseDropsZimbabweAndEverythingAfterRates(t *testing.T) {
	want := []string{"USD", "ZAR", "SZL", "JPY", "CNH", "CNY"}
	if got := bases(parsed(t)); !slices.Equal(got, want) {
		t.Errorf("bases = %v, want %v", got, want)
	}
}

func TestParseStampsGivenDate(t *testing.T) {
	for _, r := range parsed(t) {
		if !r.Date.Equal(parseDate) {
			t.Fatalf("date %v, want %v", r.Date, parseDate)
		}
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 9, 2), adapter.Date(2026, 9, 4))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
