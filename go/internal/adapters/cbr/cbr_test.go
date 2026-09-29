package cbr

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
	a := New(vcrtest.Client(t, "cbr", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI), vcrtest.AllowPlaybackRepeats))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 5))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchSinceDate(t *testing.T) {
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
		t.Errorf("got %d rates on %v, want several", n, first)
	}
}

func TestFetchForeignBaseRUBQuote(t *testing.T) {
	rates := fetch(t)
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Base == "USD" && r.Quote == "RUB" })
	if i < 0 {
		t.Fatal("no USD/RUB rate")
	}
	if rates[i].Rate <= 50 {
		t.Errorf("USD/RUB = %v, want > 50", rates[i].Rate)
	}
}

func TestFetchMetalsAgainstRUB(t *testing.T) {
	var bases []string
	for _, r := range fetch(t) {
		if !slices.Contains([]string{"XAU", "XAG", "XPT", "XPD"}, r.Base) {
			continue
		}
		if r.Quote != "RUB" {
			t.Errorf("%s quote = %s, want RUB", r.Base, r.Quote)
		}
		bases = append(bases, r.Base)
	}
	for _, want := range []string{"XAU", "XAG", "XPT", "XPD"} {
		if !slices.Contains(bases, want) {
			t.Errorf("missing %s", want)
		}
	}
}

func TestParseDynamicRestoresTajikistaniRuble(t *testing.T) {
	xml := `<?xml version="1.0" encoding="windows-1251"?>
<ValCurs ID="R01670" DateRange1="01.09.2000" DateRange2="01.11.2000" name="Foreign Currency Market Dynamic">
  <Record Date="01.09.2000" Id="R01670"><Nominal>1000</Nominal><Value>14,1700</Value></Record>
  <Record Date="01.11.2000" Id="R01670"><Nominal>1</Nominal><Value>12,6500</Value></Record>
</ValCurs>
`
	records, err := parseDynamic([]byte(xml), "TJS")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Base != "TJR" || records[1].Base != "TJS" {
		t.Fatalf("got %+v, want TJR then TJS", records)
	}
	if math.Abs(records[0].Rate-0.01417) > 1e-9 {
		t.Errorf("TJR rate = %v, want 0.01417", records[0].Rate)
	}
	if math.Abs(records[1].Rate-12.65) > 1e-9 {
		t.Errorf("TJS rate = %v, want 12.65", records[1].Rate)
	}
}

func TestParseMetalsNormalizesToTroyOunce(t *testing.T) {
	xml := `<?xml version="1.0" encoding="windows-1251"?>
<Metall FromDate="20260424" ToDate="20260424" name="Precious metals quotations">
  <Record Date="24.04.2026" Code="1"><Buy>11409,47</Buy><Sell>11409,47</Sell></Record>
  <Record Date="24.04.2026" Code="2"><Buy>187,82</Buy><Sell>187,82</Sell></Record>
  <Record Date="24.04.2026" Code="3"><Buy>5009,28</Buy><Sell>5009,28</Sell></Record>
  <Record Date="24.04.2026" Code="4"><Buy>3750,95</Buy><Sell>3750,95</Sell></Record>
</Metall>
`
	records, err := parseMetals([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(records, func(r adapter.Rate) bool { return r.Base == "XAU" })
	if i < 0 {
		t.Fatal("no XAU")
	}
	if want := 11409.47 * adapter.GramsPerTroyOunce; math.Abs(records[i].Rate-want) > 0.0001 {
		t.Errorf("XAU = %v, want %v", records[i].Rate, want)
	}
	if len(records) != 4 {
		t.Errorf("got %d records, want 4", len(records))
	}
}

func TestParseMetalsSkipsWeekends(t *testing.T) {
	xml := `<?xml version="1.0" encoding="windows-1251"?>
<Metall FromDate="20260425" ToDate="20260426" name="Precious metals quotations">
  <Record Date="25.04.2026" Code="1"><Buy>11409,47</Buy><Sell>11409,47</Sell></Record>
  <Record Date="26.04.2026" Code="1"><Buy>11409,47</Buy><Sell>11409,47</Sell></Record>
</Metall>
`
	records, err := parseMetals([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Errorf("got %+v, want none", records)
	}
}

func TestParseDynamicMatchesRubyParsing(t *testing.T) {
	xml := `<?xml version="1.0" encoding="windows-1251"?>
<ValCurs>
  <Record Date="2.3.2026"><Nominal>1</Nominal><Value>80,5</Value><VunitRate>81,25</VunitRate></Record>
  <Record Date="03.03.2026"><Nominal> 10x</Nominal><Value> 12,5 </Value><VunitRate>  </VunitRate></Record>
  <Record Date="04.03.2026"><Nominal>1</Nominal><Value></Value></Record>
  <Record Date="07.03.2026"><Nominal>1</Nominal><Value>1</Value></Record>
  <Record Date="05.03.2026"><Value>5</Value></Record>
</ValCurs>
`
	records, err := parseDynamic([]byte(xml), "USD")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("got %+v, want 3 records", records)
	}
	if !records[0].Date.Equal(adapter.Date(2026, 3, 2)) || records[0].Rate != 81.25 {
		t.Errorf("VunitRate row = %+v, want 2026-03-02 at 81.25", records[0])
	}
	if records[1].Rate != 1.25 {
		t.Errorf("Value/Nominal row = %v, want 1.25", records[1].Rate)
	}
	if !math.IsInf(records[2].Rate, 1) {
		t.Errorf("missing Nominal = %v, want +Inf as in Ruby", records[2].Rate)
	}
}

func TestParseDynamicErrors(t *testing.T) {
	for name, rec := range map[string]string{
		"bad date":      `<Record Date="2026-03-02"><Nominal>1</Nominal><Value>1</Value></Record>`,
		"bad value":     `<Record Date="02.03.2026"><Nominal>1</Nominal><Value>abc</Value></Record>`,
		"bad vunitrate": `<Record Date="02.03.2026"><VunitRate>Inf</VunitRate></Record>`,
	} {
		if _, err := parseDynamic([]byte("<ValCurs>"+rec+"</ValCurs>"), "USD"); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestParseMetalsSkipsUnknownAndBadBuy(t *testing.T) {
	xml := `<?xml version="1.0" encoding="windows-1251"?>
<Metall>
  <Record Date="24.04.2026" Code="9"><Buy>1,0</Buy></Record>
  <Record Date="24.04.2026" Code="1"><Buy></Buy></Record>
  <Record Date="24.04.2026" Code="2"><Buy>abc</Buy></Record>
  <Record Date="24.04.2026" Code="3"><Buy>0</Buy></Record>
  <Record Date="24.04.2026" Code="4"></Record>
  <Record Date="24.04.2026" Code="1"><Buy> 2,5 </Buy></Record>
</Metall>
`
	records, err := parseMetals([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Base != "XAU" || records[0].Rate != 2.5*adapter.GramsPerTroyOunce {
		t.Errorf("got %+v, want one XAU row", records)
	}
}

// Recorded with: golden.rb --repeats cbr cbr method,uri 'fetch(after:
// Date.new(2026, 3, 1), upto: Date.new(2026, 3, 5))'
func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 5))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
