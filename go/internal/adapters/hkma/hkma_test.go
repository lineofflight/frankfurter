package hkma

import (
	"context"
	"slices"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "hkma", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host, vcrtest.Path)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 2, 25), adapter.Date(2026, 2, 28))
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
		t.Errorf("got %d rates on %s, want more than 1", n, first.Format("2006-01-02"))
	}
}

func TestParseBaseQuoteRate(t *testing.T) {
	rates, err := parse([]map[string]any{
		{"end_of_day": "2026-02-28", "usd": 7.8245, "eur": 9.245, "gbp": 10.549},
	})
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Base == "USD" })
	if i < 0 {
		t.Fatal("no USD rate")
	}
	want := adapter.Rate{Date: adapter.Date(2026, 2, 28), Base: "USD", Quote: "HKD", Rate: 7.8245}
	if rates[i] != want {
		t.Errorf("USD rate = %+v, want %+v", rates[i], want)
	}
}

func TestParseAllCurrencyFields(t *testing.T) {
	rates, err := parse([]map[string]any{{
		"end_of_day": "2026-02-28",
		"usd":        7.8245,
		"eur":        9.245,
		"gbp":        10.549,
		"jpy":        0.05012,
		"cad":        5.7355,
		"aud":        5.5855,
		"sgd":        6.1855,
		"twd":        0.255,
		"chf":        10.169,
		"cny":        1.14015,
		"krw":        0.005433,
		"thb":        0.25235,
		"myr":        2.01045,
		"php":        0.1345,
		"inr":        0.0855,
		"idr":        0.0004666,
		"zar":        0.4915,
	}})
	if err != nil {
		t.Fatal(err)
	}
	var bases []string
	for _, r := range rates {
		bases = append(bases, r.Base)
	}
	slices.Sort(bases)
	want := []string{"AUD", "CAD", "CHF", "CNY", "EUR", "GBP", "IDR", "INR", "JPY", "KRW", "MYR", "PHP", "SGD", "THB",
		"TWD", "USD", "ZAR"}
	if !slices.Equal(bases, want) {
		t.Errorf("bases = %v, want %v", bases, want)
	}
}

func TestParseSkipsNullValues(t *testing.T) {
	rates, err := parse([]map[string]any{
		{"end_of_day": "1999-12-31", "usd": 7.7915, "dem": nil, "eur": nil},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 || rates[0].Base != "USD" {
		t.Errorf("rates = %+v, want only USD", rates)
	}
}

func TestParseSkipsMissingEndOfDay(t *testing.T) {
	rates, err := parse([]map[string]any{
		{"usd": 7.8, "eur": 9.2},
		{"end_of_day": "2026-02-28", "usd": 7.8245},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 {
		t.Errorf("got %d rates, want 1", len(rates))
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 2, 25), adapter.Date(2026, 2, 28))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
