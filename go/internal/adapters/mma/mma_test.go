package mma

import (
	"context"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func newAdapter(t *testing.T) *Adapter {
	return New(vcrtest.Client(t, "mma", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host), vcrtest.AllowPlaybackRepeats))
}

func fetchWindow(t *testing.T) []adapter.Rate {
	t.Helper()
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(2026, 5, 14), adapter.Date(2026, 5, 21))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func mustParse(t *testing.T, data string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetch(t *testing.T) {
	rates := fetchWindow(t)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	if rates[0].Base != "USD" || rates[0].Quote != "MVR" {
		t.Errorf("first rate = %+v, want USD/MVR", rates[0])
	}
}

func TestFetchFiltersToWindow(t *testing.T) {
	for _, r := range fetchWindow(t) {
		if r.Date.Before(adapter.Date(2026, 5, 14)) || r.Date.After(adapter.Date(2026, 5, 21)) {
			t.Errorf("date %s outside window", r.Date.Format(time.DateOnly))
		}
	}
}

func TestParse(t *testing.T) {
	rates := mustParse(t, `[
		{"Date": "21 May 2026", "Rate": "15.42"},
		{"Date": "20 May 2026", "Rate": "15.42"}
	]`)
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want 2", len(rates))
	}
	want := adapter.Rate{Date: adapter.Date(2026, 5, 21), Base: "USD", Quote: "MVR", Rate: 15.42}
	if rates[0] != want {
		t.Errorf("first rate = %+v, want %+v", rates[0], want)
	}
}

func TestParseCoercesNumericRate(t *testing.T) {
	rates := mustParse(t, `[{"Date": "21 May 2026", "Rate": 15.42}]`)
	if len(rates) != 1 || rates[0].Rate != 15.42 {
		t.Errorf("got %+v, want one rate of 15.42", rates)
	}
}

func TestParseSkipsMissingValues(t *testing.T) {
	rates := mustParse(t, `[
		{"Date": "21 May 2026", "Rate": "15.42"},
		{"Date": "20 May 2026"},
		{"Rate": "15.42"}
	]`)
	if len(rates) != 1 {
		t.Errorf("got %d rates, want 1", len(rates))
	}
}

func TestParseSkipsZeroRates(t *testing.T) {
	rates := mustParse(t, `[
		{"Date": "21 May 2026", "Rate": "0"},
		{"Date": "20 May 2026", "Rate": "15.42"}
	]`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	if !rates[0].Date.Equal(adapter.Date(2026, 5, 20)) {
		t.Errorf("date = %s, want 2026-05-20", rates[0].Date.Format(time.DateOnly))
	}
}

func TestParseDeduplicatesSameDate(t *testing.T) {
	rates := mustParse(t, `[
		{"Date": "21 May 2026", "Rate": "15.42"},
		{"Date": "21 May 2026", "Rate": "15.50"},
		{"Date": "20 May 2026", "Rate": "15.41"}
	]`)
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want 2", len(rates))
	}
	if !rates[0].Date.Equal(adapter.Date(2026, 5, 21)) || rates[0].Rate != 15.42 {
		t.Errorf("first rate = %+v, want 2026-05-21 at 15.42", rates[0])
	}
}

// Not in the Ruby spec: the raise for a non-array payload and Float()'s raise on a malformed rate.
func TestParseErrors(t *testing.T) {
	for _, data := range []string{`{"error": true}`, `[{"Date": "21 May 2026", "Rate": "n/a"}]`} {
		if _, err := parse([]byte(data)); err == nil {
			t.Errorf("parse(%s): want an error", data)
		}
	}
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file  string
		after time.Time
	}{
		{"testdata/golden/fetch.json", adapter.Date(2026, 5, 14)},
		{"testdata/golden/history.json", time.Time{}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			g := golden.Load(t, tc.file)
			rates, err := New(g.Client(t)).Fetch(context.Background(), tc.after, adapter.Date(2026, 5, 21))
			if err != nil {
				t.Fatal(err)
			}
			g.Check(t, rates)
		})
	}
}
