package bdi

import (
	"context"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

const header = "Country,Currency,ISO Code,UIC Code,Rate,Rate convention,Reference date (CET)\n"

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "bdi", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 2, 9), adapter.Date(2026, 2, 11))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchWithDateRange(t *testing.T) {
	if len(fetch(t)) == 0 {
		t.Fatal("no rates")
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
		t.Errorf("got %d rates on %s, want more than 1", n, first.Format("2006-01-02"))
	}
}

func TestParseBaseAndQuote(t *testing.T) {
	rates, err := parse([]byte(header + "UNITED STATES,Dollar,USD,001,1.1894,Foreign currency amount for 1 Euro.,2026-02-10\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	want := adapter.Rate{Date: adapter.Date(2026, 2, 10), Base: "EUR", Quote: "USD", Rate: 1.1894}
	if rates[0] != want {
		t.Errorf("rate = %+v, want %+v", rates[0], want)
	}
}

func TestParseSkips(t *testing.T) {
	for name, row := range map[string]string{
		"N.A. rates":             "BELARUS,Belarussian Ruble (new),BYN,280,N.A.,Foreign currency amount for 1 Euro.,2026-02-10\n",
		"zero rates":             "UNITED STATES,Dollar,USD,001,0,Foreign currency amount for 1 Euro.,2026-02-10\n",
		"invalid currency codes": "INVALID,Currency,XX,999,1.5,Foreign currency amount for 1 Euro.,2026-02-10\n",
		"empty CSV":              "",
		"empty rates":            "UNITED STATES,Dollar,USD,001,,Foreign currency amount for 1 Euro.,2026-02-10\n",
		"empty dates":            "UNITED STATES,Dollar,USD,001,1.1894,Foreign currency amount for 1 Euro.,\n",
		"missing columns":        "UNITED STATES,Dollar,USD,001\n",
	} {
		t.Run(name, func(t *testing.T) {
			rates, err := parse([]byte(header + row))
			if err != nil {
				t.Fatal(err)
			}
			if len(rates) != 0 {
				t.Errorf("got %d rates, want none", len(rates))
			}
		})
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 2, 9), adapter.Date(2026, 2, 11))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

func TestParseErrors(t *testing.T) {
	for name, row := range map[string]string{
		"bad rate": "UNITED STATES,Dollar,USD,001,abc,Foreign currency amount for 1 Euro.,2026-02-10\n",
		"NaN rate": "UNITED STATES,Dollar,USD,001,NaN,Foreign currency amount for 1 Euro.,2026-02-10\n",
		"bad date": "UNITED STATES,Dollar,USD,001,1.1894,Foreign currency amount for 1 Euro.,not a date\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parse([]byte(header + row)); err == nil {
				t.Error("want error")
			}
		})
	}
}
