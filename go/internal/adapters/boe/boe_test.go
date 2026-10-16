package boe

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
	a := New(vcrtest.Client(t, "boe", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 17), adapter.Date(2026, 3, 20))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchWithDateRange(t *testing.T) {
	start, upto := adapter.Date(2026, 3, 17), adapter.Date(2026, 3, 20)
	rates := fetch(t)
	dates := map[time.Time]bool{}
	for _, r := range rates {
		dates[r.Date] = true
		if r.Date.Before(start) || r.Date.After(upto) {
			t.Errorf("date %s outside %s..%s", r.Date.Format(time.DateOnly), start.Format(time.DateOnly), upto.Format(time.DateOnly))
		}
	}
	if len(dates) < 1 {
		t.Error("no dates")
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
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

func TestParseBaseAndQuote(t *testing.T) {
	rates, err := parse([]byte("DATE,XUDLUSS,XUDLERS\n17 Mar 2026,1.3343,1.1577\n"))
	if err != nil {
		t.Fatal(err)
	}
	find := func(quote string) adapter.Rate {
		for _, r := range rates {
			if r.Quote == quote {
				return r
			}
		}
		t.Fatalf("no %s rate", quote)
		return adapter.Rate{}
	}
	usd, eur := find("USD"), find("EUR")
	if usd.Base != "GBP" || usd.Rate != 1.3343 {
		t.Errorf("USD = %+v", usd)
	}
	if eur.Base != "GBP" || eur.Rate != 1.1577 {
		t.Errorf("EUR = %+v", eur)
	}
	if !usd.Date.Equal(adapter.Date(2026, 3, 17)) {
		t.Errorf("date = %s", usd.Date)
	}
}

func TestParseSkipsEmptyValues(t *testing.T) {
	rates, err := parse([]byte("DATE,XUDLUSS,XUDLERS\n17 Mar 2026,1.3343,\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 || rates[0].Quote != "USD" {
		t.Errorf("got %+v, want one USD rate", rates)
	}
}

func TestParseSkipsZeroRates(t *testing.T) {
	rates, err := parse([]byte("DATE,XUDLUSS\n17 Mar 2026,0\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %+v, want none", rates)
	}
}

func TestFetchQueryShape(t *testing.T) {
	a := New(vcrtest.Client(t, "boe", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 17), adapter.Date(2026, 3, 20))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 104 {
		t.Errorf("got %d rates, want 104", len(rates))
	}
}

func TestFetchRequiresAfter(t *testing.T) {
	a := New(vcrtest.Client(t, "boe"))
	if _, err := a.Fetch(context.Background(), time.Time{}, adapter.Date(2026, 3, 20)); err == nil {
		t.Error("want error for zero after")
	}
}

func TestParseRejectsMalformedRates(t *testing.T) {
	for _, rate := range []string{"abc", "NaN", "Inf"} {
		if _, err := parse([]byte("DATE,XUDLUSS\n17 Mar 2026," + rate + "\n")); err == nil {
			t.Errorf("rate %q: want error", rate)
		}
	}
}

func TestParseSkipsRowsWithoutDate(t *testing.T) {
	rates, err := parse([]byte("DATE,XUDLUSS\n,1.3343\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %+v, want none", rates)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 17), adapter.Date(2026, 3, 20))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
