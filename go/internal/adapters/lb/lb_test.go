package lb

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
	a := New(vcrtest.Client(t, "lb", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2014, 12, 29), adapter.Date(2014, 12, 31))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func mustParse(t *testing.T, xml string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func bases(rates []adapter.Rate) []string {
	var bs []string
	for _, r := range rates {
		bs = append(bs, r.Base)
	}
	return bs
}

func TestFetchPreEURRates(t *testing.T) {
	rates := fetch(t)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	if rates[0].Quote != "LTL" {
		t.Errorf("quote = %s, want LTL", rates[0].Quote)
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

func TestParseLTType(t *testing.T) {
	rates := mustParse(t, `<?xml version="1.0" encoding="utf-8"?>
<FxRates xmlns="http://www.lb.lt/WebServices/FxRates">
  <FxRate>
    <Tp>LT</Tp>
    <Dt>2014-12-30</Dt>
    <CcyAmt>
      <Ccy>LTL</Ccy>
      <Amt>7.6881</Amt>
    </CcyAmt>
    <CcyAmt>
      <Ccy>AED</Ccy>
      <Amt>10</Amt>
    </CcyAmt>
  </FxRate>
</FxRates>`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "AED" || r.Quote != "LTL" {
		t.Errorf("pair = %s/%s, want AED/LTL", r.Base, r.Quote)
	}
	if math.Abs(r.Rate-0.76881) > 0.00001 {
		t.Errorf("rate = %v, want 0.76881", r.Rate)
	}
	if !r.Date.Equal(adapter.Date(2014, 12, 30)) {
		t.Errorf("date = %v, want 2014-12-30", r.Date)
	}
}

func TestParseRestoresOldManat(t *testing.T) {
	rates := mustParse(t, `<?xml version="1.0" encoding="utf-8"?>
<FxRates xmlns="http://www.lb.lt/WebServices/FxRates">
  <FxRate>
    <Tp>LT</Tp>
    <Dt>2005-12-30</Dt>
    <CcyAmt><Ccy>LTL</Ccy><Amt>0.63014</Amt></CcyAmt>
    <CcyAmt><Ccy>AZN</Ccy><Amt>1000</Amt></CcyAmt>
  </FxRate>
  <FxRate>
    <Tp>LT</Tp>
    <Dt>2006-01-09</Dt>
    <CcyAmt><Ccy>LTL</Ccy><Amt>3.1077</Amt></CcyAmt>
    <CcyAmt><Ccy>AZN</Ccy><Amt>1</Amt></CcyAmt>
  </FxRate>
</FxRates>`)
	if got := bases(rates); !slices.Equal(got, []string{"AZM", "AZN"}) {
		t.Fatalf("bases = %v, want [AZM AZN]", got)
	}
	if math.Abs(rates[0].Rate-0.00063014) > 1e-9 {
		t.Errorf("rate = %v, want 0.00063014", rates[0].Rate)
	}
}

func TestParseRestoresOldTurkmenManat(t *testing.T) {
	rates := mustParse(t, `<?xml version="1.0" encoding="utf-8"?>
<FxRates xmlns="http://www.lb.lt/WebServices/FxRates">
  <FxRate>
    <Tp>LT</Tp>
    <Dt>2008-12-31</Dt>
    <CcyAmt><Ccy>LTL</Ccy><Amt>1.7354</Amt></CcyAmt>
    <CcyAmt><Ccy>TMT</Ccy><Amt>10000</Amt></CcyAmt>
  </FxRate>
  <FxRate>
    <Tp>LT</Tp>
    <Dt>2009-01-01</Dt>
    <CcyAmt><Ccy>LTL</Ccy><Amt>8.6770</Amt></CcyAmt>
    <CcyAmt><Ccy>TMT</Ccy><Amt>10</Amt></CcyAmt>
  </FxRate>
</FxRates>`)
	if got := bases(rates); !slices.Equal(got, []string{"TMM", "TMT"}) {
		t.Fatalf("bases = %v, want [TMM TMT]", got)
	}
	if math.Abs(rates[0].Rate-0.00017354) > 1e-9 {
		t.Errorf("rate = %v, want 0.00017354", rates[0].Rate)
	}
}

func TestParseEUType(t *testing.T) {
	rates := mustParse(t, `<?xml version="1.0" encoding="utf-8"?>
<FxRates xmlns="http://www.lb.lt/WebServices/FxRates">
  <FxRate>
    <Tp>EU</Tp>
    <Dt>2025-03-17</Dt>
    <CcyAmt>
      <Ccy>EUR</Ccy>
      <Amt>1</Amt>
    </CcyAmt>
    <CcyAmt>
      <Ccy>AUD</Ccy>
      <Amt>1.7160</Amt>
    </CcyAmt>
  </FxRate>
</FxRates>`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "EUR" || r.Quote != "AUD" {
		t.Errorf("pair = %s/%s, want EUR/AUD", r.Base, r.Quote)
	}
	if math.Abs(r.Rate-1.7160) > 0.0001 {
		t.Errorf("rate = %v, want 1.7160", r.Rate)
	}
	if !r.Date.Equal(adapter.Date(2025, 3, 17)) {
		t.Errorf("date = %v, want 2025-03-17", r.Date)
	}
}

func TestParseNormalizesByQuantity(t *testing.T) {
	rates := mustParse(t, `<?xml version="1.0" encoding="utf-8"?>
<FxRates xmlns="http://www.lb.lt/WebServices/FxRates">
  <FxRate>
    <Tp>LT</Tp>
    <Dt>2014-12-30</Dt>
    <CcyAmt>
      <Ccy>LTL</Ccy>
      <Amt>4.8611</Amt>
    </CcyAmt>
    <CcyAmt>
      <Ccy>AFN</Ccy>
      <Amt>100</Amt>
    </CcyAmt>
  </FxRate>
</FxRates>`)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	if math.Abs(rates[0].Rate-0.048611) > 0.000001 {
		t.Errorf("rate = %v, want 0.048611", rates[0].Rate)
	}
}

func TestParseEmptyResponse(t *testing.T) {
	rates := mustParse(t, `<?xml version="1.0" encoding="utf-8"?>
<FxRates xmlns="http://www.lb.lt/WebServices/FxRates" />`)
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2014, 12, 29), adapter.Date(2014, 12, 31))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
