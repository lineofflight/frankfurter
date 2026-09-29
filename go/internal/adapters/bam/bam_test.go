package bam

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
	vcrtest.SetSecrets(t)
	a := New(vcrtest.Client(t, "bam", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host, vcrtest.Path)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 25), adapter.Date(2026, 3, 27))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func mustParse(t *testing.T, json string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(json))
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

func TestParseBaseAndQuote(t *testing.T) {
	rates := mustParse(t, `[{"date": "2026-03-25T12:30:00", "libDevise": "USD", "moyen": 9.3794, "uniteDevise": 1}]`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "USD" || r.Quote != "MAD" {
		t.Errorf("got %s/%s, want USD/MAD", r.Base, r.Quote)
	}
	if math.Abs(r.Rate-9.3794) > 0.0001 {
		t.Errorf("rate = %v, want 9.3794", r.Rate)
	}
	if !r.Date.Equal(adapter.Date(2026, 3, 25)) {
		t.Errorf("date = %v, want 2026-03-25", r.Date)
	}
}

func TestParseNormalizesByUniteDevise(t *testing.T) {
	rates := mustParse(t, `[{"date": "2026-03-25T12:30:00", "libDevise": "JPY", "moyen": 6.2500, "uniteDevise": 100}]`)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	if math.Abs(rates[0].Rate-0.0625) > 0.0001 {
		t.Errorf("rate = %v, want 0.0625", rates[0].Rate)
	}
}

func TestParseSkips(t *testing.T) {
	tests := map[string]string{
		"zero rates":             `[{"date": "2026-03-25T12:30:00", "libDevise": "USD", "moyen": 0.0, "uniteDevise": 1}]`,
		"invalid currency codes": `[{"date": "2026-03-25T12:30:00", "libDevise": "XY", "moyen": 9.3794, "uniteDevise": 1}]`,
		"empty response":         `[]`,
	}
	for name, json := range tests {
		t.Run(name, func(t *testing.T) {
			if rates := mustParse(t, json); len(rates) != 0 {
				t.Errorf("got %d rates, want none", len(rates))
			}
		})
	}
}

func TestParseFallsBackToBuySellAverage(t *testing.T) {
	rates := mustParse(t, `[{"date": "1999-01-04T14:00:00", "libDevise": "USD", "achat": 9.2125, "vente": 9.2679, "uniteDevise": 1}]`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	if math.Abs(rates[0].Rate-9.2402) > 0.0001 {
		t.Errorf("rate = %v, want 9.2402", rates[0].Rate)
	}
}

func TestParseRejectsNonArray(t *testing.T) {
	if _, err := parse([]byte(`{"error": true}`)); err == nil {
		t.Error("want an error for a JSON object")
	}
}

func TestGolden(t *testing.T) {
	vcrtest.SetSecrets(t)
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 3, 25), adapter.Date(2026, 3, 27))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
