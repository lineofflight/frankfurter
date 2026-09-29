package cbtt

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
	a := New(vcrtest.Client(t, "cbtt", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 2), adapter.Date(2026, 3, 6))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchWithDateRange(t *testing.T) {
	rates := fetch(t)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	var dates []string
	for _, r := range rates {
		d := r.Date.Format("2006-01-02")
		if !slices.Contains(dates, d) {
			dates = append(dates, d)
		}
	}
	want := []string{"2026-03-02", "2026-03-03", "2026-03-04", "2026-03-05", "2026-03-06"}
	if !slices.Equal(dates, want) {
		t.Errorf("dates = %v, want %v", dates, want)
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	var sample []adapter.Rate
	for _, r := range fetch(t) {
		if r.Date.Equal(adapter.Date(2026, 3, 2)) {
			sample = append(sample, r)
		}
	}
	if len(sample) <= 1 {
		t.Fatalf("got %d rates on 2026-03-02, want more than 1", len(sample))
	}
	var bases []string
	for _, r := range sample {
		bases = append(bases, r.Base)
		if r.Quote != "TTD" {
			t.Errorf("quote = %q, want TTD", r.Quote)
		}
	}
	if !slices.Contains(bases, "USD") {
		t.Errorf("bases %v lack USD", bases)
	}
}

func TestParseMidOfBuyingAndSelling(t *testing.T) {
	rates, err := parse([]byte(`{"cbttdailyforexrates":[{"famedate":"2026-09-08","USD_Buying":"6.7039","USD_Selling":"6.7990"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if !r.Date.Equal(adapter.Date(2026, 9, 8)) || r.Base != "USD" || r.Quote != "TTD" || r.Rate != 6.75145 {
		t.Errorf("rate = %+v", r)
	}
}

func TestParseMapsEuroToEUR(t *testing.T) {
	rates, err := parse([]byte(`{"cbttdailyforexrates":[{"famedate":"2026-09-08","Euro_Buying":"8.5534","Euro_Selling":"8.6140"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 || rates[0].Base != "EUR" {
		t.Errorf("rates = %+v, want one EUR rate", rates)
	}
}

func TestParseReturnsNothing(t *testing.T) {
	tests := map[string]string{
		"skips a currency when either leg is missing": `{"cbttdailyforexrates":[{"famedate":"2026-09-08","CHF_Buying":null,"CHF_Selling":"9.0349","USD_Buying":"6.7039","USD_Selling":null}]}`,
		"skips zero rates":            `{"cbttdailyforexrates":[{"famedate":"1996-08-01","GYD_Buying":"0.0000","GYD_Selling":"0.0000"}]}`,
		"handles a span with no rows": `{"cbttdailyforexrates":{"famedate":"2027-2027","USD_Buying":null,"USD_Selling":null}}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			rates, err := parse([]byte(body))
			if err != nil {
				t.Fatal(err)
			}
			if len(rates) != 0 {
				t.Errorf("got %d rates, want none", len(rates))
			}
		})
	}
}

func TestParseRejectsUnexpectedPayload(t *testing.T) {
	if _, err := parse([]byte(`{"code":"rest_no_route"}`)); err == nil {
		t.Error("want an error")
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 3, 2), adapter.Date(2026, 3, 6))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
