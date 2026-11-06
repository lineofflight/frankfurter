package nbg

import (
	"context"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "nbg", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 2), adapter.Date(2026, 3, 4))
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

func TestFetchDateRange(t *testing.T) {
	if len(fetch(t)) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t)
	n := 0
	for _, r := range rates {
		if r.Date.Equal(rates[0].Date) {
			n++
		}
	}
	if n <= 1 {
		t.Errorf("got %d rates for %v, want more than 1", n, rates[0].Date)
	}
}

func TestParseBaseAndQuote(t *testing.T) {
	rates := mustParse(t, `[{"date": "2026-03-02T00:00:00", "currencies": [
  {"code": "USD", "quantity": 1, "rate": 2.7345, "name": "US Dollar",
   "diff": 0.001, "date": "2026-03-02T00:00:00", "validFromDate": "2026-03-03T00:00:00"}
]}]`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "USD" || r.Quote != "GEL" || math.Abs(r.Rate-2.7345) > 0.0001 {
		t.Errorf("rate = %+v", r)
	}
}

func TestParseNormalizesByQuantity(t *testing.T) {
	rates := mustParse(t, `[{"date": "2026-03-02T00:00:00", "currencies": [
  {"code": "JPY", "quantity": 100, "rate": 1.8200, "name": "Japanese Yen",
   "diff": 0.001, "date": "2026-03-02T00:00:00", "validFromDate": "2026-03-03T00:00:00"}
]}]`)
	if len(rates) == 0 || math.Abs(rates[0].Rate-0.0182) > 0.0001 {
		t.Errorf("rates = %+v, want 0.0182", rates)
	}
}

func TestParseSkips(t *testing.T) {
	tests := []struct{ name, json string }{
		{"zero rates", `[{"date": "2026-03-02T00:00:00", "currencies": [
  {"code": "USD", "quantity": 1, "rate": 0.0, "name": "US Dollar",
   "diff": 0.0, "date": "2026-03-02T00:00:00", "validFromDate": "2026-03-03T00:00:00"}
]}]`},
		{"invalid currency codes", `[{"date": "2026-03-02T00:00:00", "currencies": [
  {"code": "XX", "quantity": 1, "rate": 1.5, "name": "Invalid",
   "diff": 0.0, "date": "2026-03-02T00:00:00", "validFromDate": "2026-03-03T00:00:00"}
]}]`},
		{"empty response", `[]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rates := mustParse(t, tt.json); len(rates) != 0 {
				t.Errorf("got %d rates, want none", len(rates))
			}
		})
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 3, 2), adapter.Date(2026, 3, 4))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchRequestsEachDayButSunday(t *testing.T) {
	var got []string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = append(got, r.URL.String())
		return &http.Response{StatusCode: 200, Header: http.Header{},
			Body: io.NopCloser(strings.NewReader(`[]`)), Request: r}, nil
	})}
	// 2026-03-07 is a Saturday.
	if _, err := New(client).Fetch(context.Background(), adapter.Date(2026, 3, 7), adapter.Date(2026, 3, 9)); err != nil {
		t.Fatal(err)
	}
	want := []string{baseURL + "?date=2026-03-07", baseURL + "?date=2026-03-09"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

func TestFetchRequiresAfter(t *testing.T) {
	if _, err := New(http.DefaultClient).Fetch(context.Background(), time.Time{}, adapter.Date(2026, 3, 4)); err == nil {
		t.Error("want error for zero after")
	}
}

func TestParseSkipsZeroQuantity(t *testing.T) {
	rates := mustParse(t, `[{"date": "2026-03-02T00:00:00", "currencies": [
  {"code": "USD", "quantity": 0, "rate": 2.7},
  {"code": "EUR", "quantity": "1", "rate": "3.1"}
]}]`)
	if len(rates) != 1 || rates[0].Base != "EUR" || rates[0].Rate != 3.1 ||
		!rates[0].Date.Equal(adapter.Date(2026, 3, 2)) {
		t.Errorf("rates = %+v, want only EUR 3.1 on 2026-03-02", rates)
	}
}

func TestParseRejectsNonArray(t *testing.T) {
	if _, err := parse([]byte(`{"date": "2026-03-02"}`)); err == nil {
		t.Error("want error for non-array JSON")
	}
}
