package cnb

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
	a := New(vcrtest.Client(t, "cnb", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 16), adapter.Date(2026, 3, 20))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func uniqueDates(rates []adapter.Rate) []string {
	var dates []string
	seen := map[string]bool{}
	for _, r := range rates {
		d := r.Date.Format(time.DateOnly)
		if !seen[d] {
			seen[d] = true
			dates = append(dates, d)
		}
	}
	return dates
}

func TestFetchWithDateRange(t *testing.T) {
	if dates := uniqueDates(fetch(t)); len(dates) < 3 {
		t.Errorf("got %d dates, want at least 3", len(dates))
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t)
	dates := uniqueDates(rates)
	if len(dates) == 0 {
		t.Fatal("no rates")
	}
	n := 0
	for _, r := range rates {
		if r.Date.Format(time.DateOnly) == dates[0] {
			n++
		}
	}
	if n <= 1 {
		t.Errorf("got %d rates on %s, want more than 1", n, dates[0])
	}
}

func TestParseBaseAndQuote(t *testing.T) {
	rates, err := parse([]byte(`{"rates":[{"validFor":"2026-03-17","currencyCode":"USD","amount":1,"rate":22.5}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	want := adapter.Rate{Date: adapter.Date(2026, 3, 17), Base: "USD", Quote: "CZK", Rate: 22.5}
	if rates[0] != want {
		t.Errorf("got %+v, want %+v", rates[0], want)
	}
}

func TestParseNormalizesByAmount(t *testing.T) {
	tests := []struct {
		name, json  string
		want, delta float64
	}{
		{"amount 100", `{"rates":[{"validFor":"2026-03-17","currencyCode":"HUF","amount":100,"rate":6.246}]}`, 0.06246, 0.00001},
		{"amount 1000", `{"rates":[{"validFor":"2026-03-17","currencyCode":"IDR","amount":1000,"rate":1.256}]}`, 0.001256, 0.000001},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rates, err := parse([]byte(tt.json))
			if err != nil {
				t.Fatal(err)
			}
			if len(rates) == 0 {
				t.Fatal("no rates")
			}
			if got := rates[0].Rate; math.Abs(got-tt.want) > tt.delta {
				t.Errorf("rate = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseSkipsZeroRate(t *testing.T) {
	rates, err := parse([]byte(`{"rates":[{"validFor":"2026-03-17","currencyCode":"USD","amount":1,"rate":0}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseEmptyYearBeforeFirstFixing(t *testing.T) {
	rates, err := parse([]byte(`{"rates":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseRaisesOnMalformedDates(t *testing.T) {
	_, err := parse([]byte(`{"rates":[{"validFor":"not-a-date","currencyCode":"USD","amount":1,"rate":22.5}]}`))
	if err == nil {
		t.Error("want an error for a malformed date")
	}
}

func TestParseRejectsMissingRates(t *testing.T) {
	if _, err := parse([]byte(`{"error":true}`)); err == nil {
		t.Error("want an error without a rates array")
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 3, 16), adapter.Date(2026, 3, 20))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
