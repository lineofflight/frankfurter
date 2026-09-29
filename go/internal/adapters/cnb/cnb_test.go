package cnb

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"
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

func TestParseSkipsInvalidCodesAndZeroAmount(t *testing.T) {
	rates, err := parse([]byte(`{"rates":[
		{"validFor":"2026-03-17","currencyCode":"usd","amount":1,"rate":22.5},
		{"validFor":"2026-03-17","currencyCode":"XDRX","amount":1,"rate":22.5},
		{"validFor":"not-a-date","currencyCode":"EUR","amount":0,"rate":24.5},
		{"validFor":"2026-03-17","currencyCode":"GBP","rate":29.1},
		{"validFor":"2026-03-17","currencyCode":"JPY","amount":100,"rate":14.1}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 || rates[0].Base != "JPY" {
		t.Errorf("got %+v, want only JPY", rates)
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchRequestsEachYear(t *testing.T) {
	var requests []string
	client := &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.URL.String())
		year := r.URL.Query().Get("year")
		body := fmt.Sprintf(`{"rates":[
			{"validFor":"%s-01-02","currencyCode":"USD","amount":1,"rate":22},
			{"validFor":"%s-12-31","currencyCode":"USD","amount":1,"rate":23}
		]}`, year, year)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}

	rates, err := New(client).Fetch(context.Background(), adapter.Date(2024, 12, 31), adapter.Date(2026, 1, 2))
	if err != nil {
		t.Fatal(err)
	}

	wantRequests := []string{
		baseURL + "?lang=EN&year=2024",
		baseURL + "?lang=EN&year=2025",
		baseURL + "?lang=EN&year=2026",
	}
	if !slices.Equal(requests, wantRequests) {
		t.Errorf("requests = %v, want %v", requests, wantRequests)
	}
	var dates []string
	for _, r := range rates {
		dates = append(dates, r.Date.Format(time.DateOnly))
	}
	// Both bounds are inclusive.
	want := []string{"2024-12-31", "2025-01-02", "2025-12-31", "2026-01-02"}
	if !slices.Equal(dates, want) {
		t.Errorf("dates = %v, want %v", dates, want)
	}
}

func TestFetchNeedsStartDate(t *testing.T) {
	if _, err := New(http.DefaultClient).Fetch(context.Background(), time.Time{}, adapter.Date(2026, 1, 2)); err == nil {
		t.Error("want an error without a start date")
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
