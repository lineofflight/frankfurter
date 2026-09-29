package sbp

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T, after, upto time.Time) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "sbp", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host, vcrtest.Path)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func april(t *testing.T) []adapter.Rate {
	return fetch(t, adapter.Date(2026, 4, 1), adapter.Date(2026, 4, 30))
}

func TestFetchArchiveAndCurrent(t *testing.T) {
	if len(april(t)) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchEmitsPKRQuote(t *testing.T) {
	for _, r := range april(t) {
		if r.Quote != "PKR" {
			t.Fatalf("quote = %q, want PKR", r.Quote)
		}
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := april(t)
	first := rates[0].Date
	n := 0
	for _, r := range rates {
		if r.Date.Equal(first) {
			n++
		}
	}
	if n <= 10 {
		t.Errorf("got %d currencies on %s, want more than 10", n, first.Format(time.DateOnly))
	}
}

func TestFetchUSDInPlausibleRange(t *testing.T) {
	rates := april(t)
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool {
		return r.Base == "USD" && r.Date.Equal(adapter.Date(2026, 4, 1))
	})
	if i < 0 {
		t.Fatal("no USD rate on 2026-04-01")
	}
	if rate := rates[i].Rate; rate <= 100 || rate >= 500 {
		t.Errorf("USD/PKR = %v, want between 100 and 500", rate)
	}
}

func TestFetchCovers23Currencies(t *testing.T) {
	var bases []string
	for _, r := range april(t) {
		bases = append(bases, r.Base)
	}
	for _, iso := range []string{
		"AED", "AUD", "BHD", "CAD", "CHF", "CNY", "DKK", "EUR", "GBP", "HKD", "JPY", "KWD",
		"MYR", "NOK", "NZD", "OMR", "QAR", "SAR", "SEK", "SGD", "THB", "TRY", "USD",
	} {
		if !slices.Contains(bases, iso) {
			t.Errorf("missing %s", iso)
		}
	}
}

func dateRange(rates []adapter.Rate) (lo, hi time.Time) {
	for i, r := range rates {
		if i == 0 || r.Date.Before(lo) {
			lo = r.Date
		}
		if i == 0 || r.Date.After(hi) {
			hi = r.Date
		}
	}
	return lo, hi
}

func TestFetchCoversPostRestructurePeriodFromArchive(t *testing.T) {
	lo, hi := dateRange(fetch(t, adapter.Date(2026, 6, 1), adapter.Date(2026, 6, 30)))
	if !lo.Equal(adapter.Date(2026, 6, 1)) {
		t.Errorf("min date = %s, want 2026-06-01", lo.Format(time.DateOnly))
	}
	if !hi.Equal(adapter.Date(2026, 6, 30)) {
		t.Errorf("max date = %s, want 2026-06-30", hi.Format(time.DateOnly))
	}
}

func TestFetchRespectsBounds(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 4, 14), adapter.Date(2026, 4, 17))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	lo, hi := dateRange(rates)
	if lo.Before(adapter.Date(2026, 4, 14)) {
		t.Errorf("min date = %s, want >= 2026-04-14", lo.Format(time.DateOnly))
	}
	if hi.After(adapter.Date(2026, 4, 17)) {
		t.Errorf("max date = %s, want <= 2026-04-17", hi.Format(time.DateOnly))
	}
}

func TestParseMapsLabelSpellingVariants(t *testing.T) {
	for label, want := range map[string]string{
		"singaporian dollar":  "SGD",
		"singapore dollar":    "SGD",
		"japnese yen":         "JPY",
		"japanese yen":        "JPY",
		"uk pound sterling":   "GBP",
		"u.k. pound sterling": "GBP",
	} {
		if got := currencies[label]; got != want {
			t.Errorf("currencies[%q] = %q, want %q", label, got, want)
		}
	}
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file        string
		after, upto time.Time
	}{
		{"april", adapter.Date(2026, 4, 1), adapter.Date(2026, 4, 30)},
		{"june", adapter.Date(2026, 6, 1), adapter.Date(2026, 6, 30)},
		{"bounds", adapter.Date(2026, 4, 14), adapter.Date(2026, 4, 17)},
	} {
		t.Run(tc.file, func(t *testing.T) {
			g := golden.Load(t, "testdata/golden/"+tc.file+".json")
			rates, err := New(g.Client(t)).Fetch(context.Background(), tc.after, tc.upto)
			if err != nil {
				t.Fatal(err)
			}
			g.Check(t, rates)
		})
	}
}
