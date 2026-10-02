package bob

import (
	"context"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func newAdapter(t *testing.T) *Adapter {
	return New(vcrtest.Client(t, "bob", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
}

func TestFetchesRates(t *testing.T) {
	rates, err := newAdapter(t).Fetch(context.Background(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchesRatesSinceDate(t *testing.T) {
	after := adapter.Date(2026, 3, 1)
	rates, err := newAdapter(t).Fetch(context.Background(), after, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if r.Date.Before(after) {
			t.Errorf("rate dated %s before %s", r.Date.Format(time.DateOnly), after.Format(time.DateOnly))
		}
	}
}

func TestFetchesMultipleCurrenciesPerDate(t *testing.T) {
	rates, err := newAdapter(t).Fetch(context.Background(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
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

func TestParseRequiresDateColumn(t *testing.T) {
	if _, err := parse([]byte("CHN,EUR\n0.5,0.06\n")); err == nil {
		t.Error("want an error without a Date column")
	}
}

func TestParseSkipsEmptyAndZeroValues(t *testing.T) {
	rates, err := parse([]byte("\uFEFFDate,CHN,EUR,SDR,YEN\r\n\"19 Mar 2026\",0.5165,,0.0551,0\r\n\"1 Mar 2026\",0,,1,0\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []adapter.Rate{{Date: adapter.Date(2026, 3, 19), Base: "BWP", Quote: "CNY", Rate: 0.5165}}
	if len(rates) != len(want) || rates[0] != want[0] {
		t.Errorf("got %+v, want %+v", rates, want)
	}
}

func TestParseAcceptsFullMonthNames(t *testing.T) {
	rates, err := parse([]byte("Date,USD\n\"1 March 2026\",0.0748\n\"02 mar 2026\",0.075\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 || !rates[0].Date.Equal(adapter.Date(2026, 3, 1)) || !rates[1].Date.Equal(adapter.Date(2026, 3, 2)) {
		t.Errorf("got %+v", rates)
	}
}

func TestParseRejectsInvalidValues(t *testing.T) {
	for _, v := range []string{"NaN", "Inf", "abc", " "} {
		if _, err := parse([]byte("Date,USD\n\"1 Mar 2026\"," + v + "\n")); err == nil {
			t.Errorf("%q: want an error", v)
		}
	}
	if _, err := parse([]byte("Date,USD\n\"1 Sept 2026\",0.07\n")); err == nil {
		t.Error("want an error for a bad date")
	}
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file  string
		after time.Time
	}{
		{"testdata/golden/fetch.json", time.Time{}},
		{"testdata/golden/fetch_after.json", adapter.Date(2026, 3, 1)},
	} {
		t.Run(tc.file, func(t *testing.T) {
			g := golden.Load(t, tc.file)
			rates, err := New(g.Client(t)).Fetch(context.Background(), tc.after, time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			g.Check(t, rates)
		})
	}
}
