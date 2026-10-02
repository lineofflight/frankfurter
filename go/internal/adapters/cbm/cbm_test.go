package cbm

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "cbm", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetch(t *testing.T) {
	if len(fetch(t)) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchMultipleCurrencies(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range fetch(t) {
		seen[r.Base] = true
	}
	if len(seen) <= 10 {
		t.Errorf("got %d currencies, want more than 10", len(seen))
	}
}

func TestParseBaseAndQuote(t *testing.T) {
	rates, err := parse([]byte(`{"timestamp":"1775721600","rates":{"USD":"2100.00","EUR":"2449.65"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want 2", len(rates))
	}
	want := adapter.Rate{Date: adapter.Date(2026, 4, 9), Base: "USD", Quote: "MMK", Rate: 2100.0}
	if rates[0] != want {
		t.Errorf("first rate = %+v, want %+v", rates[0], want)
	}
}

func TestParseSkipsZeroRates(t *testing.T) {
	rates, err := parse([]byte(`{"timestamp":"1775721600","rates":{"USD":"2100.00","XYZ":"0.00"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 || rates[0].Base != "USD" {
		t.Errorf("got %+v, want only USD", rates)
	}
}

func TestParseRaisesWhenDataMissing(t *testing.T) {
	_, err := parse([]byte(`{"info":"test"}`))
	if err == nil || !strings.Contains(err.Error(), "timestamp or rates missing") {
		t.Errorf("got %v, want a missing-data error", err)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

func TestParseSkipsMMK(t *testing.T) {
	rates, err := parse([]byte(`{"timestamp":1775721600,"rates":{"MMK":"1","USD":2100}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := adapter.Rate{Date: adapter.Date(2026, 4, 9), Base: "USD", Quote: "MMK", Rate: 2100}
	if len(rates) != 1 || rates[0] != want {
		t.Errorf("got %+v, want only %+v", rates, want)
	}
}

func TestParseTimestampLikeRubyInteger(t *testing.T) {
	for _, ts := range []string{`" 1775721600 "`, `1775721600.9`} {
		rates, err := parse([]byte(`{"timestamp":` + ts + `,"rates":{"USD":"2100"}}`))
		if err != nil {
			t.Fatalf("%s: %v", ts, err)
		}
		if rates[0].Date != adapter.Date(2026, 4, 9) {
			t.Errorf("%s: date = %v", ts, rates[0].Date)
		}
	}
	if _, err := parse([]byte(`{"timestamp":"1775721600.0","rates":{"USD":"2100"}}`)); err == nil {
		t.Error("want an error for a decimal timestamp string")
	}
}

func TestParseRejectsBadRates(t *testing.T) {
	for _, rates := range []string{
		`{"USD":"NaN"}`, `{"USD":"Infinity"}`, `{"USD":"abc"}`, `{"USD":null}`,
		`{"USD":"2100","USD":"2200"}`,
	} {
		if _, err := parse([]byte(`{"timestamp":"1775721600","rates":` + rates + `}`)); err == nil {
			t.Errorf("%s: want an error", rates)
		}
	}
}
