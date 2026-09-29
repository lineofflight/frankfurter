package boj

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
	a := New(vcrtest.Client(t, "boj", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 15))
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

func TestFetchBothSeriesPerDate(t *testing.T) {
	rates := fetch(t)
	first := rates[0].Date
	n := 0
	for _, r := range rates {
		if r.Date.Equal(first) {
			n++
		}
	}
	if n != 2 {
		t.Errorf("got %d rates on %s, want 2", n, first.Format("2006-01-02"))
	}
}

func filter(rates []adapter.Rate, base string) []adapter.Rate {
	var out []adapter.Rate
	for _, r := range rates {
		if r.Base == base {
			out = append(out, r)
		}
	}
	return out
}

func TestParseResultSet(t *testing.T) {
	rates, err := parse([]byte(`{"RESULTSET": [
		{"SERIES_CODE": "FXERD04", "VALUES": {"SURVEY_DATES": [20260303, 20260304, 20260305], "VALUES": [149.50, 150.10, 151.00]}},
		{"SERIES_CODE": "FXERD34", "VALUES": {"SURVEY_DATES": [20260303, 20260304, 20260305], "VALUES": [1.0820, 1.0845, 1.0900]}}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 6 {
		t.Fatalf("got %d rates, want 6", len(rates))
	}

	usdJPY := filter(rates, "USD")
	if len(usdJPY) != 3 {
		t.Fatalf("got %d USD rates, want 3", len(usdJPY))
	}
	if usdJPY[0].Quote != "JPY" {
		t.Errorf("quote = %s, want JPY", usdJPY[0].Quote)
	}
	if math.Abs(usdJPY[0].Rate-149.50) > 0.001 {
		t.Errorf("rate = %v, want 149.50", usdJPY[0].Rate)
	}

	eurUSD := filter(rates, "EUR")
	if eurUSD[0].Quote != "USD" {
		t.Errorf("quote = %s, want USD", eurUSD[0].Quote)
	}
	if math.Abs(eurUSD[0].Rate-1.0820) > 0.001 {
		t.Errorf("rate = %v, want 1.0820", eurUSD[0].Rate)
	}
}

func TestParseSkipsNilValues(t *testing.T) {
	rates, err := parse([]byte(`{"RESULTSET": [
		{"SERIES_CODE": "FXERD04", "VALUES": {"SURVEY_DATES": [20260301, 20260302, 20260303], "VALUES": [null, null, 149.50]}}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	if math.Abs(rates[0].Rate-149.50) > 0.001 {
		t.Errorf("rate = %v, want 149.50", rates[0].Rate)
	}
}

func TestParseSkipsUnknownSeriesCodes(t *testing.T) {
	rates, err := parse([]byte(`{"RESULTSET": [
		{"SERIES_CODE": "UNKNOWN99", "VALUES": {"SURVEY_DATES": [20260303], "VALUES": [1.23]}}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 15))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
