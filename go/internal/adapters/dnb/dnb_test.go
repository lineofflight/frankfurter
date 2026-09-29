package dnb

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "dnb", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2025, 3, 3), adapter.Date(2025, 3, 7))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func uniqueDates(rates []adapter.Rate) []time.Time {
	var dates []time.Time
	seen := map[time.Time]bool{}
	for _, r := range rates {
		if !seen[r.Date] {
			seen[r.Date] = true
			dates = append(dates, r.Date)
		}
	}
	return dates
}

func TestFetchWithDateRange(t *testing.T) {
	if n := len(uniqueDates(fetch(t))); n < 3 {
		t.Errorf("got %d dates, want at least 3", n)
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t)
	dates := uniqueDates(rates)
	if len(dates) == 0 {
		t.Fatal("no rates")
	}
	n := 0
	for _, r := range rates {
		if r.Date.Equal(dates[0]) {
			n++
		}
	}
	if n <= 1 {
		t.Errorf("got %d rates on %s, want more than 1", n, dates[0].Format(time.DateOnly))
	}
}

func mustParse(t *testing.T, csv string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestParseBaseAndQuote(t *testing.T) {
	rates := mustParse(t, "VALUTA;KURTYP;TID;INDHOLD\nUSD;KBH;2025M03D03;712.6900\n")
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "USD" || r.Quote != "DKK" {
		t.Errorf("got %s/%s, want USD/DKK", r.Base, r.Quote)
	}
	if math.Abs(r.Rate-7.1269) > 0.0001 {
		t.Errorf("rate = %v, want 7.1269", r.Rate)
	}
	if !r.Date.Equal(adapter.Date(2025, 3, 3)) {
		t.Errorf("date = %v, want 2025-03-03", r.Date)
	}
}

func TestParseDividesRateBy100(t *testing.T) {
	rates := mustParse(t, "VALUTA;KURTYP;TID;INDHOLD\nEUR;KBH;2025M03D03;745.8300\n")
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	if math.Abs(rates[0].Rate-7.4583) > 0.0001 {
		t.Errorf("rate = %v, want 7.4583", rates[0].Rate)
	}
}

func TestParseSkips(t *testing.T) {
	tests := []struct{ name, csv string }{
		{"missing values marked as ..", "VALUTA;KURTYP;TID;INDHOLD\nDEM;KBH;1977M01D03;..\n"},
		{"zero rate", "VALUTA;KURTYP;TID;INDHOLD\nUSD;KBH;2025M03D03;0.0000\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rates := mustParse(t, tt.csv); len(rates) != 0 {
				t.Errorf("got %d rates, want none", len(rates))
			}
		})
	}
}

func TestParseHandlesBOM(t *testing.T) {
	rates := mustParse(t, "\xEF\xBB\xBFVALUTA;KURTYP;TID;INDHOLD\nUSD;KBH;2025M03D03;712.6900\n")
	if len(rates) != 1 {
		t.Errorf("got %d rates, want 1", len(rates))
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2025, 3, 3), adapter.Date(2025, 3, 7))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
