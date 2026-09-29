package tcmb

import (
	"context"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	vcrtest.SetSecrets(t)
	a := New(vcrtest.Client(t, "tcmb", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 22))
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
		t.Errorf("got %d rates on %s, want more than 1", n, first.Format("2006-01-02"))
	}
}

func TestFetchKeepsEveryDigitOfMid(t *testing.T) {
	// JPY is quoted per 100 units, so its rate lands three orders of magnitude below the rest of the feed. Rounding to
	// a fixed number of decimal places clipped it: buy 28.0072 and sell 28.1927 average to 28.09995, which is
	// 0.2809995 per yen, and a round(4) stored 0.281.
	for _, r := range fetch(t) {
		if r.Base == "JPY" && r.Date.Equal(adapter.Date(2026, 3, 2)) {
			if r.Rate != 0.2809995 {
				t.Errorf("JPY rate = %v, want 0.2809995", r.Rate)
			}
			return
		}
	}
	t.Fatal("no JPY rate on 2026-03-02")
}

func TestGolden(t *testing.T) {
	vcrtest.SetSecrets(t)
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 22))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
