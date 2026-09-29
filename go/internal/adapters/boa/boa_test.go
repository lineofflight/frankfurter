package boa

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
	a := New(vcrtest.Client(t, "boa", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host, vcrtest.Path)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func find(rates []adapter.Rate, base string, date time.Time) *adapter.Rate {
	for i, r := range rates {
		if r.Base == base && (date.IsZero() || r.Date.Equal(date)) {
			return &rates[i]
		}
	}
	return nil
}

func TestFetchUsesDZDAsQuote(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 4, 28), adapter.Date(2026, 4, 30))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if r.Quote != "DZD" {
			t.Fatalf("quote = %s, want DZD", r.Quote)
		}
	}
}

func TestFetchCoversMultipleBases(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 4, 28), adapter.Date(2026, 4, 30))
	var bases []string
	for _, r := range rates {
		bases = append(bases, r.Base)
	}
	for _, want := range []string{"USD", "EUR", "GBP", "JPY"} {
		if !slices.Contains(bases, want) {
			t.Errorf("bases missing %s", want)
		}
	}
}

func TestFetchMapsEuroSheetToEUR(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 4, 28), adapter.Date(2026, 4, 30))
	eur := find(rates, "EUR", adapter.Date(2026, 4, 30))
	if eur == nil {
		t.Fatal("no EUR rate on 2026-04-30")
	}
	if math.Abs(eur.Rate-154.76) > 0.5 {
		t.Errorf("EUR rate = %v, want about 154.76", eur.Rate)
	}
}

func TestFetchNormalisesJPYPerUnit(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 4, 28), adapter.Date(2026, 4, 30))
	jpy := find(rates, "JPY", adapter.Date(2026, 4, 30))
	if jpy == nil {
		t.Fatal("no JPY rate on 2026-04-30")
	}
	if math.Abs(jpy.Rate-0.828) > 0.05 {
		t.Errorf("JPY rate = %v, want about 0.828", jpy.Rate)
	}
}

func TestFetchFiltersByDateRange(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 4, 28), adapter.Date(2026, 4, 30))
	lo, hi := rates[0].Date, rates[0].Date
	for _, r := range rates {
		if r.Date.Before(lo) {
			lo = r.Date
		}
		if r.Date.After(hi) {
			hi = r.Date
		}
	}
	if !lo.Equal(adapter.Date(2026, 4, 28)) {
		t.Errorf("min date = %s, want 2026-04-28", lo)
	}
	if !hi.Equal(adapter.Date(2026, 4, 30)) {
		t.Errorf("max date = %s, want 2026-04-30", hi)
	}
}

func TestFetchUSDInPlausibleRange(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 4, 30), adapter.Date(2026, 4, 30))
	usd := find(rates, "USD", time.Time{})
	if usd == nil {
		t.Fatal("no USD rate")
	}
	if math.Abs(usd.Rate-132.5) > 5.0 {
		t.Errorf("USD rate = %v, want about 132.5", usd.Rate)
	}
}

// The Ruby spec stubs download to return a hub page without the link; archiveURL is that step.
func TestArchiveLinkMissingFromHub(t *testing.T) {
	if _, err := archiveURL([]byte("<html><body>no link here</body></html>")); err == nil {
		t.Fatal("want an error")
	}
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file        string
		after, upto time.Time
	}{
		{"fetch.json", adapter.Date(2026, 4, 28), adapter.Date(2026, 4, 30)},
		{"fetch_one_day.json", adapter.Date(2026, 4, 30), adapter.Date(2026, 4, 30)},
	} {
		t.Run(tc.file, func(t *testing.T) {
			g := golden.Load(t, "testdata/golden/"+tc.file)
			rates, err := New(g.Client(t)).Fetch(context.Background(), tc.after, tc.upto)
			if err != nil {
				t.Fatal(err)
			}
			g.Check(t, rates)
		})
	}
}
