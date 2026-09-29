package nbm

import (
	"context"
	"math"
	"slices"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "nbm", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 4, 6), adapter.Date(2026, 4, 8))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func find(rates []adapter.Rate, base string) (adapter.Rate, bool) {
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Base == base })
	if i < 0 {
		return adapter.Rate{}, false
	}
	return rates[i], true
}

func mustParse(t *testing.T, f func([]byte) ([]adapter.Rate, error), xml string) []adapter.Rate {
	t.Helper()
	rates, err := f([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchDateRange(t *testing.T) {
	if rates := fetch(t); len(rates) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t)
	first := rates[0].Date
	n := 0
	for _, r := range rates {
		if r.Date.Equal(first) && !isMetal(r.Base) {
			n++
		}
	}
	if n <= 1 {
		t.Errorf("got %d currencies on %v, want several", n, first)
	}
}

func TestFetchForeignBaseMDLQuote(t *testing.T) {
	usd, ok := find(fetch(t), "USD")
	if !ok || usd.Quote != "MDL" {
		t.Fatalf("no USD/MDL rate: %+v", usd)
	}
	if usd.Rate <= 10 {
		t.Errorf("USD rate = %v, want > 10", usd.Rate)
	}
}

func TestFetchXAUAndXAGAgainstMDL(t *testing.T) {
	var bases []string
	for _, r := range fetch(t) {
		if !isMetal(r.Base) {
			continue
		}
		if !slices.Contains(bases, r.Base) {
			bases = append(bases, r.Base)
		}
		if r.Quote != "MDL" {
			t.Errorf("%s quote = %s, want MDL", r.Base, r.Quote)
		}
	}
	for _, want := range []string{"XAU", "XAG"} {
		if !slices.Contains(bases, want) {
			t.Errorf("missing %s", want)
		}
	}
}

func TestParseMetalsNormalizesToTroyOunce(t *testing.T) {
	rates := mustParse(t, parseMetals, `<?xml version="1.0" encoding="UTF-8"?>
<MetalPrice Date="24.04.2026" name="The price of precious metals">
  <Metal ID="">
    <NumCode>961</NumCode>
    <CharCode>XAG</CharCode>
    <Nominal>1</Nominal>
    <Name>Gram silver</Name>
    <Value>41.5550</Value>
  </Metal>
  <Metal ID="">
    <NumCode>959</NumCode>
    <CharCode>XAU</CharCode>
    <Nominal>1</Nominal>
    <Name>Gram gold</Name>
    <Value>2619.6183</Value>
  </Metal>
</MetalPrice>`)
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want 2", len(rates))
	}
	xau, _ := find(rates, "XAU")
	xag, _ := find(rates, "XAG")
	if math.Abs(xau.Rate-2619.6183*adapter.GramsPerTroyOunce) > 0.0001 {
		t.Errorf("XAU = %v", xau.Rate)
	}
	if math.Abs(xag.Rate-41.5550*adapter.GramsPerTroyOunce) > 0.0001 {
		t.Errorf("XAG = %v", xag.Rate)
	}
	if xau.Quote != "MDL" {
		t.Errorf("XAU quote = %s", xau.Quote)
	}
}

func TestParseMetalsSkipsWeekend(t *testing.T) {
	rates := mustParse(t, parseMetals, `<?xml version="1.0" encoding="UTF-8"?>
<MetalPrice Date="25.04.2026" name="The price of precious metals">
  <Metal ID="">
    <NumCode>959</NumCode>
    <CharCode>XAU</CharCode>
    <Nominal>1</Nominal>
    <Name>Gram gold</Name>
    <Value>2619.6183</Value>
  </Metal>
</MetalPrice>`)
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParse(t *testing.T) {
	rates := mustParse(t, parse, `<?xml version="1.0" encoding="UTF-8"?>
<ValCurs Date="08.04.2026" name="Official exchange rate">
  <Valute ID="44">
    <NumCode>840</NumCode>
    <CharCode>USD</CharCode>
    <Nominal>1</Nominal>
    <Name>US Dollar</Name>
    <Value>17.4597</Value>
  </Valute>
</ValCurs>`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "USD" || r.Quote != "MDL" || math.Abs(r.Rate-17.4597) > 0.0001 {
		t.Errorf("got %+v", r)
	}
	if !r.Date.Equal(adapter.Date(2026, 4, 8)) {
		t.Errorf("date = %v", r.Date)
	}
}

func TestParseNormalizesByNominal(t *testing.T) {
	rates := mustParse(t, parse, `<?xml version="1.0" encoding="UTF-8"?>
<ValCurs Date="08.04.2026" name="Official exchange rate">
  <Valute ID="25">
    <NumCode>392</NumCode>
    <CharCode>JPY</CharCode>
    <Nominal>100</Nominal>
    <Name>Japanese Yen</Name>
    <Value>10.9277</Value>
  </Valute>
</ValCurs>`)
	if len(rates) != 1 || math.Abs(rates[0].Rate-0.109277) > 0.0001 {
		t.Errorf("got %+v", rates)
	}
}

func TestParseSkipsZeroValues(t *testing.T) {
	rates := mustParse(t, parse, `<?xml version="1.0" encoding="UTF-8"?>
<ValCurs Date="01.01.1994" name="Official exchange rate">
  <Valute ID="43">
    <NumCode>804</NumCode>
    <CharCode>UAK</CharCode>
    <Nominal>0</Nominal>
    <Name>Ukrainian Karbovanets</Name>
    <Value>0.0000</Value>
  </Valute>
</ValCurs>`)
	if len(rates) != 0 {
		t.Errorf("got %+v, want none", rates)
	}
}

func TestParseSkipsInvalidCodes(t *testing.T) {
	rates := mustParse(t, parse, `<?xml version="1.0" encoding="UTF-8"?>
<ValCurs Date="08.04.2026" name="Official exchange rate">
  <Valute ID="99">
    <NumCode>999</NumCode>
    <CharCode>XX</CharCode>
    <Nominal>1</Nominal>
    <Name>Invalid</Name>
    <Value>1.5</Value>
  </Valute>
</ValCurs>`)
	if len(rates) != 0 {
		t.Errorf("got %+v, want none", rates)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 4, 6), adapter.Date(2026, 4, 8))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
