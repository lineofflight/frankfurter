package bdi

import (
	"context"
	"slices"
	"testing"
	"time"

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

func TestParseMapsOldAfghaniToAFABeforeSwitch(t *testing.T) {
	rates, err := parse([]byte(header +
		"AFGHANISTAN (Islamic State of),Afghani,AFN,115,5806.4,Foreign currency amount for 1 Euro.,2004-03-31\n" +
		"AFGHANISTAN (Islamic State of),Afghani,AFN,115,58.52,Foreign currency amount for 1 Euro.,2004-04-01\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []adapter.Rate{
		{Date: adapter.Date(2004, 3, 31), Base: "EUR", Quote: "AFA", Rate: 5806.4},
		{Date: adapter.Date(2004, 4, 1), Base: "EUR", Quote: "AFN", Rate: 58.52},
	}
	if !slices.Equal(rates, want) {
		t.Errorf("rates = %+v, want %+v", rates, want)
	}
}

func TestParseRelabelsZimbabweDollarAcrossRedenominations(t *testing.T) {
	rates, err := parse([]byte(header +
		"ZIMBABWE,Zimbabwe Dollar,ZWD,51,108471581765.0,Foreign currency amount for 1 Euro.,2008-07-31\n" +
		"ZIMBABWE,Zimbabwe Dollar,ZWD,51,11.805092,Foreign currency amount for 1 Euro.,2008-08-01\n" +
		"ZIMBABWE,Zimbabwe Dollar,ZWD,51,15741267667.1,Foreign currency amount for 1 Euro.,2009-02-02\n" +
		"ZIMBABWE,Zimbabwe Dollar,ZWD,51,28.2678,Foreign currency amount for 1 Euro.,2009-02-03\n"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rates {
		got = append(got, r.Date.Format(time.DateOnly)+" "+r.Quote)
	}
	want := []string{"2008-07-31 ZWD", "2008-08-01 ZWR", "2009-02-02 ZWR", "2009-02-03 ZWL"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
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
