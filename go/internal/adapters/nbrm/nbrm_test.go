package nbrm

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
	a := New(vcrtest.Client(t, "nbrm", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host), vcrtest.AllowPlaybackRepeats))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 5, 30))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchSinceDate(t *testing.T) {
	if len(fetch(t)) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t)
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

func TestFetchExcludesMKDToMKD(t *testing.T) {
	for _, r := range fetch(t) {
		if r.Base == "MKD" && r.Quote == "MKD" {
			t.Fatalf("found MKD/MKD row: %+v", r)
		}
	}
}

func TestParse(t *testing.T) {
	rates, err := parse([]byte(`[
		{"oznaka": "EUR", "sreden": "61.5", "datum": "2026-03-01T00:00:00", "nomin": "1"},
		{"oznaka": "USD", "sreden": "57.2", "datum": "2026-03-01T00:00:00", "nomin": "1"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want 2", len(rates))
	}
	want := adapter.Rate{Date: adapter.Date(2026, 3, 1), Base: "EUR", Quote: "MKD", Rate: 61.5}
	if rates[0] != want {
		t.Errorf("first rate = %+v, want %+v", rates[0], want)
	}
}

func TestParseNormalizesNomin(t *testing.T) {
	rates, err := parse([]byte(`[
		{"oznaka": "JPY", "sreden": "38.8", "datum": "2010-01-04T00:00:00", "nomin": "100"},
		{"oznaka": "EUR", "sreden": "61.5", "datum": "2010-01-04T00:00:00", "nomin": "1"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, r := range rates {
		got[r.Base] = r.Rate
	}
	if jpy, ok := got["JPY"]; !ok || math.Abs(jpy-0.388) > 0.001 {
		t.Errorf("JPY = %v, want 0.388", jpy)
	}
	if eur, ok := got["EUR"]; !ok || eur != 61.5 {
		t.Errorf("EUR = %v, want 61.5", eur)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	// Ruby VCR, with allow_playback_repeats, serves unused interactions before repeating one, so the two chunk
	// requests got the two recorded responses. vcrtest's repeat mode always replays the first host match, so replay
	// by exact URI instead, which picks the same interactions Ruby did.
	a := New(vcrtest.Client(t, "nbrm", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 5, 30))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
