package cbg

import (
	"context"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T, after, upto time.Time) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "cbg", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host, vcrtest.Path)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func mustParse(t *testing.T, data, code string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(data), code)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchNarrowWindow(t *testing.T) {
	if rates := fetch(t, adapter.Date(2026, 5, 19), adapter.Date(2026, 5, 22)); len(rates) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchRespectsAfterAndUpto(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 19), adapter.Date(2026, 5, 22))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if r.Date.Before(adapter.Date(2026, 5, 20)) || r.Date.After(adapter.Date(2026, 5, 22)) {
			t.Errorf("date %s outside window", r.Date.Format(time.DateOnly))
		}
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 19), adapter.Date(2026, 5, 22))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	first := rates[0].Date
	for _, r := range rates {
		if r.Date.Before(first) {
			first = r.Date
		}
	}
	n := 0
	for _, r := range rates {
		if r.Date.Equal(first) {
			n++
		}
	}
	if n <= 5 {
		t.Errorf("got %d rates on %s, want more than 5", n, first.Format(time.DateOnly))
	}
}

func TestParseForeignBaseGMDQuote(t *testing.T) {
	rates := mustParse(t, `[[1779408000000, 72.39]]`, "USD")
	want := adapter.Rate{Date: adapter.Date(2026, 5, 22), Base: "USD", Quote: "GMD", Rate: 72.39}
	if len(rates) != 1 || rates[0] != want {
		t.Errorf("got %+v, want %+v", rates, want)
	}
}

func TestParseMillisecondEpochAsUTCDate(t *testing.T) {
	rates := mustParse(t, `[[1779408000000, 86.28]]`, "EUR")
	if len(rates) != 1 || !rates[0].Date.Equal(adapter.Date(2026, 5, 22)) {
		t.Errorf("got %+v, want date 2026-05-22", rates)
	}
}

func TestParseSkipsZeroRates(t *testing.T) {
	if rates := mustParse(t, `[[1779408000000, 0.0]]`, "USD"); len(rates) != 0 {
		t.Errorf("got %+v, want none", rates)
	}
}

func TestParseEmptyResponse(t *testing.T) {
	if rates := mustParse(t, `[]`, "USD"); len(rates) != 0 {
		t.Errorf("got %+v, want none", rates)
	}
}

func TestParseSkipsMalformedEntries(t *testing.T) {
	rates := mustParse(t, `[[1779408000000, 72.39], [null, 1.0], [1779408000000, null], "not an entry"]`, "USD")
	if len(rates) != 1 || rates[0].Rate != 72.39 {
		t.Errorf("got %+v, want one rate of 72.39", rates)
	}
}

func TestParseRejectsNonArray(t *testing.T) {
	if _, err := parse([]byte(`{"error": true}`), "USD"); err == nil {
		t.Error("want an error for a JSON object")
	}
}

func TestFetchUSDPlausibleForMay2026(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 21), adapter.Date(2026, 5, 22))
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool {
		return r.Base == "USD" && r.Date.Equal(adapter.Date(2026, 5, 22))
	})
	if i < 0 {
		t.Fatal("no USD rate on 2026-05-22")
	}
	if got := rates[i].Rate; math.Abs(got-72.39) > 5.0 {
		t.Errorf("USD/GMD = %v, want about 72.39", got)
	}
}

func TestCurrenciesExcludeWAUA(t *testing.T) {
	if slices.Contains(currencies, "WAUA") {
		t.Error("currencies include WAUA")
	}
}

func TestCurrenciesExcludeGMD(t *testing.T) {
	if slices.Contains(currencies, "GMD") {
		t.Error("currencies include GMD")
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 5, 19), adapter.Date(2026, 5, 22))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
