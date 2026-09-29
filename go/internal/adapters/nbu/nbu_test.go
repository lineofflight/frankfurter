package nbu

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

// newAdapter pins today to the cassette's end date; Ruby's spec relies on host-only matching instead.
func newAdapter(t *testing.T) *Adapter {
	a := New(vcrtest.Client(t, "nbu", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	a.Now = func() time.Time { return time.Date(2026, 3, 16, 12, 0, 0, 0, time.UTC) }
	return a
}

func TestFetchesRatesSinceDate(t *testing.T) {
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(2026, 3, 1), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("expected rates")
	}
}

func TestFetchesMultipleCurrenciesPerDate(t *testing.T) {
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(2026, 3, 1), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("expected rates")
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

func TestParseRestoresTajikistaniRubleBeforeSomoni(t *testing.T) {
	rates, err := parse([]byte(`[
		{"exchangedate": "01.09.2000", "cc": "TJS", "units": 1000, "rate": 2.7776},
		{"exchangedate": "02.12.2002", "cc": "TJS", "units": 1, "rate": 1.805263}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 || rates[0].Base != "TJR" || rates[1].Base != "TJS" {
		t.Fatalf("got %+v, want bases TJR, TJS", rates)
	}
	if math.Abs(rates[0].Rate-0.0027776) > 1e-9 {
		t.Errorf("rate = %v, want 0.0027776", rates[0].Rate)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	a.Now = g.Now(t)
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
