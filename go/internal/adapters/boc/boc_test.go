package boc

import (
	"context"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func TestFetch(t *testing.T) {
	a := New(vcrtest.Client(t, "boc", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2025, 1, 1), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
}

func TestParseStoresForeignAsBaseAndCADAsQuote(t *testing.T) {
	rates, err := parse([]byte(`{"observations": [{"d": "2026-03-20", "FXUSDCAD": {"v": "1.3728"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	want := adapter.Rate{Date: adapter.Date(2026, 3, 20), Base: "USD", Quote: "CAD", Rate: 1.3728}
	if rates[0] != want {
		t.Errorf("first rate = %+v, want %+v", rates[0], want)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2025, 1, 1), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
