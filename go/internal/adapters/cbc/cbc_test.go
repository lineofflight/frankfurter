package cbc

import (
	"context"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func newAdapter(t *testing.T) *Adapter {
	return New(vcrtest.Client(t, "cbc", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host, vcrtest.Path)))
}

func fetchFebruary(t *testing.T) []adapter.Rate {
	t.Helper()
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(2026, 2, 1), adapter.Date(2026, 2, 5))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func toRows(rows ...[]string) [][]any {
	out := make([][]any, len(rows))
	for i, row := range rows {
		for _, v := range row {
			out[i] = append(out[i], v)
		}
	}
	return out
}

var modernRow = []string{
	"20260201", "32.500", "150.25", "1.2500", "7.8000", "1200.0", "1.3500", "1.3200", "7.2000", "0.6500",
	"15800.0", "33.500", "4.4000", "56.000", "1.0500", "-", "-", "-", "25000.0",
}

var legacyRow = []string{
	"19930105", "25.405", "125.25", "1.5499", "7.7427", "788.2", "1.2766", "1.6560", "7.7200", "0.6732",
	"2048.0", "25.550", "2.5908", "-", "-", "1.6255", "5.5425", "1.8258", "-",
}

func mustParse(t *testing.T, rows [][]any, start, end time.Time) []adapter.Rate {
	t.Helper()
	rates, err := parse(rows, start, end)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func find(rates []adapter.Rate, match func(adapter.Rate) bool) *adapter.Rate {
	for i := range rates {
		if match(rates[i]) {
			return &rates[i]
		}
	}
	return nil
}

func byQuote(q string) func(adapter.Rate) bool {
	return func(r adapter.Rate) bool { return r.Quote == q }
}
func byBase(b string) func(adapter.Rate) bool {
	return func(r adapter.Rate) bool { return r.Base == b }
}

func TestFetchWithDateRange(t *testing.T) {
	if len(fetchFebruary(t)) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetchFebruary(t)
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

func TestParseXUSDConvention(t *testing.T) {
	twd := find(mustParse(t, toRows(modernRow), time.Time{}, time.Time{}), byQuote("TWD"))
	if twd == nil {
		t.Fatal("no TWD rate")
	}
	want := adapter.Rate{Date: adapter.Date(2026, 2, 1), Base: "USD", Quote: "TWD", Rate: 32.5}
	if *twd != want {
		t.Errorf("TWD = %+v, want %+v", *twd, want)
	}
}

func TestParseUSDXConvention(t *testing.T) {
	rates := mustParse(t, toRows(modernRow), time.Time{}, time.Time{})
	for base, want := range map[string]float64{"GBP": 1.25, "AUD": 0.65, "EUR": 1.05} {
		r := find(rates, byBase(base))
		if r == nil {
			t.Errorf("no %s rate", base)
			continue
		}
		if r.Quote != "USD" || r.Rate != want {
			t.Errorf("%s = %s %v, want USD %v", base, r.Quote, r.Rate, want)
		}
	}
}

func TestParseAllActiveColumns(t *testing.T) {
	if got := len(mustParse(t, toRows(modernRow), time.Time{}, time.Time{})); got != 15 {
		t.Errorf("got %d rates, want 15", got)
	}
}

func TestParseSkipsDashes(t *testing.T) {
	rates := mustParse(t, toRows(legacyRow), time.Time{}, time.Time{})
	if find(rates, byQuote("PHP")) != nil {
		t.Error("PHP should be skipped")
	}
	if find(rates, byBase("EUR")) != nil {
		t.Error("EUR should be skipped")
	}
	if find(rates, byQuote("VND")) != nil {
		t.Error("VND should be skipped")
	}
}

func TestParseHistoricalColumns(t *testing.T) {
	rates := mustParse(t, toRows(legacyRow), time.Time{}, time.Time{})
	for quote, want := range map[string]float64{"DEM": 1.6255, "FRF": 5.5425, "NLG": 1.8258} {
		r := find(rates, byQuote(quote))
		if r == nil {
			t.Errorf("no %s rate", quote)
			continue
		}
		if r.Base != "USD" || r.Rate != want {
			t.Errorf("%s = %s %v, want USD %v", quote, r.Base, r.Rate, want)
		}
	}
}

func TestParseFiltersByDateRange(t *testing.T) {
	dashes := []string{"-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-"}
	rows := toRows(
		append([]string{"20260201", "32.500"}, dashes...),
		append([]string{"20260205", "32.600"}, dashes...),
		append([]string{"20260210", "32.700"}, dashes...),
	)
	rates := mustParse(t, rows, adapter.Date(2026, 2, 3), adapter.Date(2026, 2, 8))
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	if !rates[0].Date.Equal(adapter.Date(2026, 2, 5)) {
		t.Errorf("date = %s, want 2026-02-05", rates[0].Date.Format(time.DateOnly))
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 2, 1), adapter.Date(2026, 2, 5))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

func TestGoldenOpenRange(t *testing.T) {
	g := golden.Load(t, "testdata/golden/all.json")
	a := New(g.Client(t))
	a.Now = g.Now(t)
	rates, err := a.Fetch(context.Background(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
