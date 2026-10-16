package nbt

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T, after, upto time.Time) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "nbt", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func mustParse(t *testing.T, xml string, expected time.Time) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(xml), expected)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchWithDateRange(t *testing.T) {
	if rates := fetch(t, adapter.Date(2026, 5, 18), adapter.Date(2026, 5, 20)); len(rates) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 18), adapter.Date(2026, 5, 20))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	first := rates[0].Date
	n := 0
	for _, r := range rates {
		if r.Date.Equal(first) {
			n++
		}
	}
	if n <= 1 {
		t.Fatalf("got %d rates on %s, want more than 1", n, first.Format(time.DateOnly))
	}
}

func TestParseValuteWithBaseAndQuote(t *testing.T) {
	rates := mustParse(t, `<?xml version="1.0" encoding="utf-8" ?>
<ValCurs Date="2026-05-20" name="Foreign Currency Market">
<Valute ID="840">
   <CharCode>USD</CharCode>
   <Nominal>1</Nominal>
   <Name>US Dollar</Name>
   <Value>9.3288</Value>
</Valute>
</ValCurs>
`, time.Time{})

	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "USD" || r.Quote != "TJS" {
		t.Errorf("got %s/%s, want USD/TJS", r.Base, r.Quote)
	}
	if math.Abs(r.Rate-9.3288) > 0.0001 {
		t.Errorf("rate = %v, want 9.3288", r.Rate)
	}
	if !r.Date.Equal(adapter.Date(2026, 5, 20)) {
		t.Errorf("date = %v, want 2026-05-20", r.Date)
	}
}

func TestParseNormalizesRateByNominal(t *testing.T) {
	rates := mustParse(t, `<?xml version="1.0" encoding="utf-8" ?>
<ValCurs Date="2026-05-20" name="Foreign Currency Market">
<Valute ID="860">
   <CharCode>UZS</CharCode>
   <Nominal>100</Nominal>
   <Name>Uzbekistan Sum</Name>
   <Value>0.0774</Value>
</Valute>
</ValCurs>
`, time.Time{})

	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	if math.Abs(rates[0].Rate-0.000774) > 0.000001 {
		t.Errorf("rate = %v, want 0.000774", rates[0].Rate)
	}
}

func TestParseTrustsCharCodeOverNumericID(t *testing.T) {
	rates := mustParse(t, `<?xml version="1.0" encoding="utf-8" ?>
<ValCurs Date="2026-05-20" name="Foreign Currency Market">
<Valute ID="810">
   <CharCode>RUB</CharCode>
   <Nominal>1</Nominal>
   <Name>Russian Ruble</Name>
   <Value>0.1308</Value>
</Valute>
</ValCurs>
`, time.Time{})

	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	if rates[0].Base != "RUB" {
		t.Errorf("base = %s, want RUB", rates[0].Base)
	}
}

func TestParseSkips(t *testing.T) {
	tests := []struct {
		name     string
		xml      string
		expected time.Time
	}{
		{
			name: "zero values",
			xml: `<?xml version="1.0" encoding="utf-8" ?>
<ValCurs Date="2026-05-20" name="Foreign Currency Market">
<Valute ID="840">
   <CharCode>USD</CharCode>
   <Nominal>1</Nominal>
   <Name>US Dollar</Name>
   <Value>0</Value>
</Valute>
</ValCurs>
`,
		},
		{
			name: "invalid currency codes",
			xml: `<?xml version="1.0" encoding="utf-8" ?>
<ValCurs Date="2026-05-20" name="Foreign Currency Market">
<Valute ID="999">
   <CharCode>XX</CharCode>
   <Nominal>1</Nominal>
   <Name>Invalid</Name>
   <Value>1.5</Value>
</Valute>
</ValCurs>
`,
		},
		{
			// Out-of-range requests silently return today's snapshot.
			name: "response date does not match requested date",
			xml: `<?xml version="1.0" encoding="utf-8" ?>
<ValCurs Date="2026-05-25" name="Foreign Currency Market">
<Valute ID="840">
   <CharCode>USD</CharCode>
   <Nominal>1</Nominal>
   <Name>US Dollar</Name>
   <Value>9.2793</Value>
</Valute>
</ValCurs>
`,
			expected: adapter.Date(2000, 1, 1),
		},
		{
			name: "empty ValCurs",
			xml: `<?xml version="1.0" encoding="utf-8" ?>
<ValCurs Date="2026-05-20" name="Foreign Currency Market" />
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rates := mustParse(t, tt.xml, tt.expected); len(rates) != 0 {
				t.Errorf("got %d rates, want none", len(rates))
			}
		})
	}
}

func TestFetchRequiresAfter(t *testing.T) {
	a := New(vcrtest.Client(t, "nbt", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	if _, err := a.Fetch(context.Background(), time.Time{}, adapter.Date(2026, 5, 20)); err == nil {
		t.Fatal("want error for zero after")
	}
}

func TestParseKeepsMatchingDateAndSkipsZeroNominal(t *testing.T) {
	rates := mustParse(t, `<ValCurs Date="2026-05-20">
<Valute><CharCode>USD</CharCode><Nominal>1</Nominal><Value>9.3288</Value></Valute>
<Valute><CharCode>UZS</CharCode><Nominal>0</Nominal><Value>0.0774</Value></Valute>
</ValCurs>`, adapter.Date(2026, 5, 20))

	if len(rates) != 1 || rates[0].Base != "USD" {
		t.Fatalf("got %+v, want only USD", rates)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 5, 18), adapter.Date(2026, 5, 20))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
