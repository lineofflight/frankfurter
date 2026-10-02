package banrep

import (
	"context"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func newAdapter(t *testing.T) *Adapter {
	return New(vcrtest.Client(t, "banrep", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host), vcrtest.AllowPlaybackRepeats))
}

func TestFetch(t *testing.T) {
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(2026, 3, 16), adapter.Date(2026, 3, 24))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
}

func TestParse(t *testing.T) {
	rates, err := parse([]byte(`[
		{"valor": "4181.69", "unidad": "COP", "vigenciadesde": "2026-03-24T00:00:00.000", "vigenciahasta": "2026-03-24T00:00:00.000"},
		{"valor": "4150.32", "unidad": "COP", "vigenciadesde": "2026-03-25T00:00:00.000", "vigenciahasta": "2026-03-25T00:00:00.000"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want 2", len(rates))
	}
	want := adapter.Rate{Date: adapter.Date(2026, 3, 24), Base: "USD", Quote: "COP", Rate: 4181.69}
	if rates[0] != want {
		t.Errorf("first rate = %+v, want %+v", rates[0], want)
	}
}

func TestParseSkipsMissingValues(t *testing.T) {
	rates, err := parse([]byte(`[
		{"valor": "4181.69", "unidad": "COP", "vigenciadesde": "2026-03-24T00:00:00.000", "vigenciahasta": "2026-03-24T00:00:00.000"},
		{"unidad": "COP", "vigenciadesde": "2026-03-25T00:00:00.000"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 {
		t.Errorf("got %d rates, want 1", len(rates))
	}
}

func TestParseRejectsNonArray(t *testing.T) {
	if _, err := parse([]byte(`{"error": true}`)); err == nil {
		t.Error("want an error for a JSON object")
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 3, 16), adapter.Date(2026, 3, 24))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
