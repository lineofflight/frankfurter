package mas

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T, after, upto time.Time) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "mas", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func fetchMarch(t *testing.T) []adapter.Rate {
	return fetch(t, adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 31))
}

func find(rates []adapter.Rate, base string) *adapter.Rate {
	for i := range rates {
		if rates[i].Base == base {
			return &rates[i]
		}
	}
	return nil
}

func TestFetch(t *testing.T) {
	if len(fetchMarch(t)) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetchMarch(t)
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

func TestFetchNormalizesPer100UnitRates(t *testing.T) {
	jpy := find(fetchMarch(t), "JPY")
	if jpy == nil {
		t.Fatal("no JPY rate")
	}
	if jpy.Rate >= 1 {
		t.Errorf("JPY rate = %v, want < 1", jpy.Rate)
	}
}

func TestFetchIncludesPerUnitCurrencies(t *testing.T) {
	eur := find(fetchMarch(t), "EUR")
	if eur == nil {
		t.Fatal("no EUR rate")
	}
	if eur.Rate <= 1 {
		t.Errorf("EUR rate = %v, want > 1", eur.Rate)
	}
}

func TestFetchRespectsDateBoundaries(t *testing.T) {
	limit := adapter.Date(2026, 3, 10)
	rates := fetch(t, adapter.Date(2026, 3, 1), limit)
	var within bool
	for _, r := range rates {
		if r.Date.After(limit) {
			t.Errorf("rate dated %s after %s", r.Date.Format(time.DateOnly), limit.Format(time.DateOnly))
		} else {
			within = true
		}
	}
	if !within {
		t.Error("no rates on or before the limit")
	}
}

func TestParseCSVData(t *testing.T) {
	rates, err := parse([]byte(`MAS: Financial Database - Exchange Rates

Exchange Rates (Daily)
Mar 2026 to Mar 2026


End of Period,,,S$ Per Unit of Euro,S$ Per Unit of US Dollar,S$ Per 100 Units of Japanese Yen,S$ Per 100 Units of Malaysian Ringgit
2026,Mar,02,1.4940,1.2674,0.8107,32.45
,,03,1.4877,1.2726,0.8091,32.42
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 8 {
		t.Fatalf("got %d rates, want 8", len(rates))
	}
	get := func(base string) adapter.Rate {
		for _, r := range rates {
			if r.Base == base && r.Date.Equal(adapter.Date(2026, 3, 2)) {
				return r
			}
		}
		t.Fatalf("no %s rate on 2026-03-02", base)
		return adapter.Rate{}
	}
	eur := get("EUR")
	if math.Abs(eur.Rate-1.4940) > 0.0001 {
		t.Errorf("EUR rate = %v, want 1.4940", eur.Rate)
	}
	if eur.Quote != "SGD" {
		t.Errorf("EUR quote = %q, want SGD", eur.Quote)
	}
	if jpy := get("JPY"); math.Abs(jpy.Rate-0.008107) > 0.00001 {
		t.Errorf("JPY rate = %v, want 0.008107", jpy.Rate)
	}
}

func TestParseSkipsEmptyValues(t *testing.T) {
	rates, err := parse([]byte("End of Period,,,S$ Per Unit of Euro\n2026,Mar,02,\n,,03,1.4877\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 {
		t.Errorf("got %d rates, want 1", len(rates))
	}
}

func TestParseNoResults(t *testing.T) {
	rates, err := parse([]byte("<html><div>No Results Found</div></html>"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseRaisesWithoutHeader(t *testing.T) {
	_, err := parse([]byte(""))
	if err == nil || !strings.Contains(err.Error(), "End of Period") {
		t.Errorf("err = %v, want one mentioning End of Period", err)
	}
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file        string
		after, upto time.Time
	}{
		{"testdata/golden/march.json", adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 31)},
		{"testdata/golden/early_march.json", adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 10)},
	} {
		t.Run(tc.file, func(t *testing.T) {
			g := golden.Load(t, tc.file)
			rates, err := New(g.Client(t)).Fetch(context.Background(), tc.after, tc.upto)
			if err != nil {
				t.Fatal(err)
			}
			g.Check(t, rates)
		})
	}
}
