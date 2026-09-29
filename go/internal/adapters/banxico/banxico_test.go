package banxico

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
	vcrtest.SetSecrets(t)
	a := New(vcrtest.Client(t, "banxico", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 15))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchesRatesSinceDate(t *testing.T) {
	if len(fetch(t)) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchesMultipleCurrenciesPerDate(t *testing.T) {
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
		t.Errorf("got %d rates on %s, want more than 1", n, first.Format(time.DateOnly))
	}
}

func TestFetchRequiresAPIKey(t *testing.T) {
	t.Setenv("BANXICO_API_KEY", "")
	if _, err := New(nil).Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 15)); err == nil {
		t.Error("want an error without an API key")
	}
}

func TestParseSeriesResponse(t *testing.T) {
	rates, err := parse([]byte(`{"bmx": {"series": [
		{"idSerie": "SF43718", "datos": [
			{"fecha": "15/03/2026", "dato": "17.1234"},
			{"fecha": "16/03/2026", "dato": "17.2345"}
		]},
		{"idSerie": "SF46410", "datos": [
			{"fecha": "15/03/2026", "dato": "18.5678"}
		]}
	]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 3 {
		t.Fatalf("got %d rates, want 3", len(rates))
	}
	if rates[0].Base != "USD" {
		t.Errorf("base = %s, want USD", rates[0].Base)
	}
	if rates[0].Quote != "MXN" {
		t.Errorf("quote = %s, want MXN", rates[0].Quote)
	}
	if math.Abs(rates[0].Rate-17.1234) > 0.001 {
		t.Errorf("rate = %v, want 17.1234", rates[0].Rate)
	}
	if !rates[0].Date.Equal(adapter.Date(2026, 3, 15)) {
		t.Errorf("date = %s, want 2026-03-15", rates[0].Date)
	}
	if rates[2].Base != "EUR" {
		t.Errorf("base = %s, want EUR", rates[2].Base)
	}
}

func TestParseCommaFormattedNumbers(t *testing.T) {
	rates, err := parse([]byte(`{"bmx": {"series": [
		{"idSerie": "SF43718", "datos": [{"fecha": "15/03/2026", "dato": "17,123.45"}]}
	]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	if math.Abs(rates[0].Rate-17123.45) > 0.001 {
		t.Errorf("rate = %v, want 17123.45", rates[0].Rate)
	}
}

func TestParseSkipsInvalidValues(t *testing.T) {
	rates, err := parse([]byte(`{"bmx": {"series": [
		{"idSerie": "SF43718", "datos": [{"fecha": "15/03/2026", "dato": "N/E"}]}
	]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestGolden(t *testing.T) {
	vcrtest.SetSecrets(t)
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 15))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
