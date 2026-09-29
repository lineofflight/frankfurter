package nb

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
	a := New(vcrtest.Client(t, "nb", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 16), adapter.Date(2026, 3, 24))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func mustParse(t *testing.T, csv string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(csv))
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

func TestParseBaseAndQuote(t *testing.T) {
	rates := mustParse(t, `FREQ,BASE_CUR,QUOTE_CUR,TENOR,TIME_PERIOD,OBS_VALUE,UNIT_MULT
B,USD,NOK,SP,2026-03-16,10.5432,0
`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	want := adapter.Rate{Date: adapter.Date(2026, 3, 16), Base: "USD", Quote: "NOK", Rate: 10.5432}
	if rates[0] != want {
		t.Errorf("got %+v, want %+v", rates[0], want)
	}
}

func TestParseAdjustsByUnitMult(t *testing.T) {
	rates := mustParse(t, `FREQ,BASE_CUR,QUOTE_CUR,TENOR,TIME_PERIOD,OBS_VALUE,UNIT_MULT
B,JPY,NOK,SP,2026-03-16,7.1234,2
`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	if math.Abs(rates[0].Rate-0.071234) > 0.000001 {
		t.Errorf("rate = %v, want 0.071234", rates[0].Rate)
	}
}

func TestParseFiltersNonBusinessDayRows(t *testing.T) {
	rates := mustParse(t, `FREQ,BASE_CUR,QUOTE_CUR,TENOR,TIME_PERIOD,OBS_VALUE,UNIT_MULT
M,USD,NOK,SP,2026-03-16,10.5432,0
B,EUR,NOK,SP,2026-03-16,11.2345,0
`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	if rates[0].Base != "EUR" {
		t.Errorf("base = %q, want EUR", rates[0].Base)
	}
}

func TestParseIndexCodesAlongsideCurrencies(t *testing.T) {
	// Preserve the provider's index observations alongside its currency rates.
	rates := mustParse(t, `FREQ,BASE_CUR,QUOTE_CUR,TENOR,TIME_PERIOD,OBS_VALUE,UNIT_MULT
B,I44,NOK,SP,2026-03-16,120.5432,0
B,TWI,NOK,SP,2026-03-16,115.432,0
B,USD,NOK,SP,2026-03-16,10.5432,0
`)
	if len(rates) != 3 {
		t.Errorf("got %d rates, want 3", len(rates))
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 16), adapter.Date(2026, 3, 24))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
