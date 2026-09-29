package nbe

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
	a := New(vcrtest.Client(t, "nbe", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), after, upto)
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
	if rates := fetch(t, adapter.Date(2026, 5, 18), adapter.Date(2026, 5, 20)); len(rates) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 18), adapter.Date(2026, 5, 20))
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
		t.Errorf("got %d rates on %s, want more than 1", n, first)
	}
}

func TestFetchSkipsWeekends(t *testing.T) {
	if rates := fetch(t, adapter.Date(2026, 5, 16), adapter.Date(2026, 5, 17)); len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseForeignBaseAndETBQuote(t *testing.T) {
	rates := mustParse(t, `{"success": true, "status": 200, "data": [
		{"buying": "159.6247", "selling": "161.2209", "date": "2026-05-21",
		 "weighted_average": "159.6247",
		 "currency": {"name": "US DOLLAR", "code": "USD"}}
	]}`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "USD" || r.Quote != "ETB" {
		t.Errorf("pair = %s/%s, want USD/ETB", r.Base, r.Quote)
	}
	if math.Abs(r.Rate-159.6247) > 0.0001 {
		t.Errorf("rate = %v, want 159.6247", r.Rate)
	}
	if !r.Date.Equal(adapter.Date(2026, 5, 21)) {
		t.Errorf("date = %s, want 2026-05-21", r.Date)
	}
}

func TestParseUsesWeightedAverageAsMid(t *testing.T) {
	rates := mustParse(t, `{"success": true, "status": 200, "data": [
		{"buying": "185.005", "selling": "186.8551", "date": "2026-05-21",
		 "weighted_average": "185.9301",
		 "currency": {"name": "EURO", "code": "EUR"}}
	]}`)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	if math.Abs(rates[0].Rate-185.9301) > 0.0001 {
		t.Errorf("rate = %v, want 185.9301", rates[0].Rate)
	}
}

func TestParsePassesXDRThrough(t *testing.T) {
	rates := mustParse(t, `{"success": true, "status": 200, "data": [
		{"buying": "218.207", "selling": "220.389", "date": "2026-05-21",
		 "weighted_average": "219.298",
		 "currency": {"name": "SDR", "code": "XDR"}}
	]}`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "XDR" || r.Quote != "ETB" {
		t.Errorf("pair = %s/%s, want XDR/ETB", r.Base, r.Quote)
	}
	if math.Abs(r.Rate-219.298) > 0.0001 {
		t.Errorf("rate = %v, want 219.298", r.Rate)
	}
}

func TestParseSkipsZeroRates(t *testing.T) {
	rates := mustParse(t, `{"success": true, "status": 200, "data": [
		{"buying": "0", "selling": "0", "date": "2026-05-21",
		 "weighted_average": "0",
		 "currency": {"name": "US DOLLAR", "code": "USD"}}
	]}`)
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseHandlesEmptyData(t *testing.T) {
	if rates := mustParse(t, `{"success": true, "status": 200, "data": []}`); len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseRejectsMissingData(t *testing.T) {
	if _, err := parse([]byte(`{"success": false}`)); err == nil {
		t.Error("want an error without a data array")
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 5, 18), adapter.Date(2026, 5, 20))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
