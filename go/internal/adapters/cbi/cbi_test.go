package cbi

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
	a := New(vcrtest.Client(t, "cbi", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchWithIQDAsQuote(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 2, 1), adapter.Date(2026, 2, 28))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if r.Quote != "IQD" {
			t.Fatalf("quote = %q, want IQD", r.Quote)
		}
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 2, 1), adapter.Date(2026, 2, 28))
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
	if n <= 5 {
		t.Errorf("got %d rates on %s, want more than 5", n, first.Format(time.DateOnly))
	}
}

func TestFetchUSDAroundOfficialPeg(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 2, 10), adapter.Date(2026, 2, 11))
	for _, r := range rates {
		if r.Base == "USD" && r.Date.Equal(adapter.Date(2026, 2, 11)) {
			if math.Abs(r.Rate-1310) > 30 {
				t.Errorf("USD/IQD = %v, want 1310 +/- 30", r.Rate)
			}
			return
		}
	}
	t.Fatal("no USD rate on 2026-02-11")
}

func TestFetchFiltersByDateRange(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 2, 5), adapter.Date(2026, 2, 10))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if !r.Date.After(adapter.Date(2026, 2, 5)) || r.Date.After(adapter.Date(2026, 2, 10)) {
			t.Errorf("date %s outside (2026-02-05, 2026-02-10]", r.Date.Format(time.DateOnly))
		}
	}
}

func TestDiscoverFileURLPicksGoldLink(t *testing.T) {
	html := `
<a href="https://cbi.iq/static/uploads/up/file-111.xlsx">الدينار تجاه الدولار</a>
<a href="https://cbi.iq/static/uploads/up/file-222.xlsx">العملات الرئيسية والذهب</a>
<a href="https://cbi.iq/static/uploads/up/file-333.xlsx">العملات الأجنبية تجاه الدولار</a>
`
	got, err := discoverFileURL([]byte(html))
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://cbi.iq/static/uploads/up/file-222.xlsx"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDiscoverFileURLFailsWithoutGoldLink(t *testing.T) {
	html := `<a href="https://cbi.iq/static/uploads/up/file-111.xlsx">USD only</a>`
	if _, err := discoverFileURL([]byte(html)); err == nil {
		t.Error("want an error when no xlsx link mentions gold")
	}
}

func TestExtractCode(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"S.FR", "CHF"},
		{"UAE", "AED"},
		{"Gold", "XAU"},
		{"SDR", "XDR"},
		{"Saudi Arabian Riyal SAR", "SAR"},
	} {
		if got := extractCode(tc.text); got != tc.want {
			t.Errorf("extractCode(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestParseSheetToleratesEmptyYear(t *testing.T) {
	rates, err := parseSheet([]byte("<worksheet><sheetData/></worksheet>"), nil, 2027)
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestMonthFromLabel(t *testing.T) {
	for _, tc := range []struct {
		label string
		want  time.Month
	}{
		{"Jan. 2026", 1},
		{"Jan.", 1},
		{"Feb.2026", 2},
		{"*Mar. 2020", 3}, // footnote marker
		{"40299", 5},      // Excel date serial
		{"June 2013", 6},
		{"July 2009", 7},
		{"Sept.2026", 9},
		{"Spet.2016", 9}, // historical typo
		{"Dec, 2009", 12},
	} {
		got, ok := monthFromLabel(tc.label)
		if !ok || got != tc.want {
			t.Errorf("monthFromLabel(%q) = %v, %v; want %v", tc.label, got, ok, tc.want)
		}
	}
}

func TestMonthFromLabelRejectsNonMonths(t *testing.T) {
	for _, label := range []string{"Average", "2026", "1"} {
		if got, ok := monthFromLabel(label); ok {
			t.Errorf("monthFromLabel(%q) = %v, want none", label, got)
		}
	}
}

// Ruby's \b is Unicode-aware, so a code glued to Arabic text is not a token.
func TestExtractCodeUnicodeBoundary(t *testing.T) {
	if got := extractCode("الدولارUSD"); got != "" {
		t.Errorf("got %q, want no code", got)
	}
	if got := extractCode("ABCDE"); got != "" {
		t.Errorf("got %q, want no code", got)
	}
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file        string
		after, upto time.Time
	}{
		{"testdata/golden/fetch.json", adapter.Date(2026, 2, 1), adapter.Date(2026, 2, 28)},
		{"testdata/golden/all.json", time.Time{}, time.Time{}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			g := golden.Load(t, tc.file)
			rates, err := New(g.Client(t)).Fetch(context.Background(), tc.after, tc.upto)
			if err != nil {
				t.Fatal(err)
			}
			g.Check(t, rates)
		})
	}
}
