package ecb

import (
	"context"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func TestFetchRates(t *testing.T) {
	a := New(vcrtest.Client(t, "ecb_historical", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2025, 1, 1), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
}

// No Ruby counterpart: covers the row filters in parse_row.
func TestParseSkipsNonDailyAndInvalidRows(t *testing.T) {
	rates, err := parse([]byte("FREQ,CURRENCY,TIME_PERIOD,OBS_VALUE\n" +
		"D,USD,2025-01-02,1.0321\n" +
		"M,USD,2025-01,1.03\n" +
		"D,usd,2025-01-02,1.0321\n" +
		"D,JPY,2025-01-02,\n" +
		"D,GBP,2025-01-02,0.8294"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want 2: %+v", len(rates), rates)
	}
	want := adapter.Rate{Date: adapter.Date(2025, 1, 2), Base: "EUR", Quote: "USD", Rate: 1.0321}
	if r := rates[0]; !r.Date.Equal(want.Date) || r.Base != want.Base || r.Quote != want.Quote || r.Rate != want.Rate {
		t.Errorf("rates[0] = %+v, want %+v", r, want)
	}
	if rates[1].Quote != "GBP" || rates[1].Rate != 0.8294 {
		t.Errorf("rates[1] = %+v", rates[1])
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
