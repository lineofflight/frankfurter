package bnrrw

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
	a := New(vcrtest.Client(t, "bnrrw", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 5, 19), adapter.Date(2026, 5, 22))
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
	if n <= 5 {
		t.Errorf("got %d rates on %s, want more than 5", n, first.Format("2006-01-02"))
	}
}

func TestFetchForeignBaseAndRWFQuote(t *testing.T) {
	var bases []string
	for _, r := range fetch(t) {
		if r.Quote != "RWF" {
			t.Errorf("quote = %q, want RWF", r.Quote)
		}
		bases = append(bases, r.Base)
	}
	if !slices.Contains(bases, "USD") {
		t.Error("no USD base")
	}
}

func TestFetchUSDInPlausibleRange(t *testing.T) {
	rates := fetch(t)
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool {
		return r.Base == "USD" && r.Date.Equal(adapter.Date(2026, 5, 22))
	})
	if i < 0 {
		t.Fatal("no USD rate on 2026-05-22")
	}
	if math.Abs(rates[i].Rate-1463.0) > 50.0 {
		t.Errorf("USD/RWF = %v, want 1463 +/- 50", rates[i].Rate)
	}
}

func TestParseTakesAverageRateAsMid(t *testing.T) {
	rates, err := parse([]byte(`[{"currency_name":"USD","buying_rate":"1458.3525","average_rate":"1463.3525","selling_rate":"1468.3525","post_date":"22-May-26"}]`))
	if err != nil {
		t.Fatal(err)
	}
	want := []adapter.Rate{{Date: adapter.Date(2026, 5, 22), Base: "USD", Quote: "RWF", Rate: 1463.3525}}
	if !slices.Equal(rates, want) {
		t.Errorf("got %+v, want %+v", rates, want)
	}
}

func TestParseThousandsCommas(t *testing.T) {
	rates, err := parse([]byte(`[{"currency_name":"USD","buying_rate":"1,254.96","average_rate":"1,267.50","selling_rate":"1,280.05","post_date":"04-Jan-24"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	if rates[0].Rate != 1267.50 {
		t.Errorf("rate = %v, want 1267.50", rates[0].Rate)
	}
	if !rates[0].Date.Equal(adapter.Date(2024, 1, 4)) {
		t.Errorf("date = %v, want 2024-01-04", rates[0].Date)
	}
}

// Covers "skips entries with non-positive rates", "skips entries with missing average_rate" and "returns an empty
// array for an empty response".
func TestParseSkips(t *testing.T) {
	tests := map[string]string{
		"non-positive rates":   `[{"currency_name":"USD","buying_rate":"0","average_rate":"0","selling_rate":"0","post_date":"22-May-26"}]`,
		"missing average_rate": `[{"currency_name":"USD","buying_rate":"1458.0","selling_rate":"1468.0","post_date":"22-May-26"}]`,
		"empty response":       `[]`,
		"false average_rate":   `[{"currency_name":"USD","average_rate":false,"post_date":"22-May-26"}]`,
		"string entry":         `["USD"]`,
		"lowercase code":       `[{"currency_name":"usd","average_rate":"1463.35","post_date":"22-May-26"}]`,
		"missing post_date":    `[{"currency_name":"USD","average_rate":"1463.35"}]`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			rates, err := parse([]byte(body))
			if err != nil {
				t.Fatal(err)
			}
			if len(rates) != 0 {
				t.Errorf("got %+v, want none", rates)
			}
		})
	}
}

func TestParseNumericAverageRate(t *testing.T) {
	rates, err := parse([]byte(`[{"currency_name":"USD","average_rate":1463.3525,"post_date":"4-MAY-26"}]`))
	if err != nil {
		t.Fatal(err)
	}
	want := []adapter.Rate{{Date: adapter.Date(2026, 5, 4), Base: "USD", Quote: "RWF", Rate: 1463.3525}}
	if !slices.Equal(rates, want) {
		t.Errorf("got %+v, want %+v", rates, want)
	}
}

func TestParseErrors(t *testing.T) {
	tests := map[string]string{
		"not an array":         `{"error":"nope"}`,
		"invalid average_rate": `[{"currency_name":"USD","average_rate":"n/a","post_date":"22-May-26"}]`,
		"invalid post_date":    `[{"currency_name":"USD","average_rate":"1463.35","post_date":"2026-05-22"}]`,
		"null entry":           `[null]`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			if rates, err := parse([]byte(body)); err == nil {
				t.Errorf("got %+v, want error", rates)
			}
		})
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 5, 19), adapter.Date(2026, 5, 22))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
