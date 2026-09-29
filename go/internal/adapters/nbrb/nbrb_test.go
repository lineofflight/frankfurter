package nbrb

import (
	"context"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "nbrb", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	// The cassette ends on 2026-03-17, the day it was recorded.
	a.Now = func() time.Time { return time.Date(2026, 3, 17, 12, 0, 0, 0, time.UTC) }
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchesRatesSinceDate(t *testing.T) {
	if len(fetch(t)) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchesMultipleCurrenciesPerDate(t *testing.T) {
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
		t.Errorf("got %d rates on %s, want more than 1", n, first.Format(time.DateOnly))
	}
}

func TestParseDynamicsSkipsWeekendsAndScales(t *testing.T) {
	rates, err := parseDynamics([]byte(`[
		{"Cur_ID":510,"Date":"2026-03-06T00:00:00","Cur_OfficialRate":7.8014},
		{"Cur_ID":510,"Date":"2026-03-07T00:00:00","Cur_OfficialRate":7.9}
	]`), currency{ID: 510, ISO: "AMD", Scale: 1000})
	if err != nil {
		t.Fatal(err)
	}
	want := adapter.Rate{Date: adapter.Date(2026, 3, 6), Base: "AMD", Quote: "BYN", Rate: 7.8014 / 1000}
	if len(rates) != 1 || rates[0] != want {
		t.Errorf("got %+v, want [%+v]", rates, want)
	}
}

func TestParseDynamicsRejectsNonArray(t *testing.T) {
	if _, err := parseDynamics([]byte(`{"error":true}`), currency{}); err == nil {
		t.Error("want an error for a JSON object")
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
