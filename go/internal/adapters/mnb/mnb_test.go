package mnb

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

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "mnb", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2025, 3, 24), adapter.Date(2025, 3, 28))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func dates(rates []adapter.Rate) []time.Time {
	var ds []time.Time
	for _, r := range rates {
		if !slices.ContainsFunc(ds, r.Date.Equal) {
			ds = append(ds, r.Date)
		}
	}
	return ds
}

func mustParse(t *testing.T, xml string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchWithDateRange(t *testing.T) {
	if n := len(dates(fetch(t))); n < 3 {
		t.Errorf("got %d dates, want at least 3", n)
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t)
	first := dates(rates)[0]
	n := 0
	for _, r := range rates {
		if r.Date.Equal(first) {
			n++
		}
	}
	if n <= 1 {
		t.Errorf("got %d rates on %v, want several", n, first)
	}
}

func TestFetchForeignBaseHUFQuote(t *testing.T) {
	rates := fetch(t)
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Base == "EUR" && r.Quote == "HUF" })
	if i < 0 {
		t.Fatal("no EUR/HUF rate")
	}
	if rates[i].Rate <= 300 {
		t.Errorf("EUR/HUF = %v, want > 300", rates[i].Rate)
	}
}

func TestParseUnitMultiplier(t *testing.T) {
	rates := mustParse(t, `<MNBExchangeRates>
  <Day date="2025-03-24">
    <Rate unit="100" curr="JPY">253,40</Rate>
  </Day>
</MNBExchangeRates>`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "JPY" || r.Quote != "HUF" {
		t.Errorf("got %s/%s, want JPY/HUF", r.Base, r.Quote)
	}
	if math.Abs(r.Rate-2.534) > 0.001 {
		t.Errorf("rate = %v, want 2.534", r.Rate)
	}
	if !r.Date.Equal(adapter.Date(2025, 3, 24)) {
		t.Errorf("date = %v, want 2025-03-24", r.Date)
	}
}

func TestParseUnitOne(t *testing.T) {
	rates := mustParse(t, `<MNBExchangeRates>
  <Day date="2025-03-24">
    <Rate unit="1" curr="EUR">398,44</Rate>
  </Day>
</MNBExchangeRates>`)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	if math.Abs(rates[0].Rate-398.44) > 0.01 {
		t.Errorf("rate = %v, want 398.44", rates[0].Rate)
	}
}

func TestParseSkipsZeroRate(t *testing.T) {
	rates := mustParse(t, `<MNBExchangeRates>
  <Day date="2025-03-24">
    <Rate unit="1" curr="EUR">0,00</Rate>
  </Day>
</MNBExchangeRates>`)
	if len(rates) != 0 {
		t.Errorf("got %v, want none", rates)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2025, 3, 24), adapter.Date(2025, 3, 28))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
