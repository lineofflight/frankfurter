package bnm

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T, upto time.Time) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "bnm", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func bases(rates []adapter.Rate) []string {
	var out []string
	for _, r := range rates {
		if !slices.Contains(out, r.Base) {
			out = append(out, r.Base)
		}
	}
	return out
}

func TestFetch(t *testing.T) {
	if len(fetch(t, adapter.Date(2026, 3, 31))) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 3, 31))
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

func TestFetchNormalizesSDRToXDR(t *testing.T) {
	got := bases(fetch(t, adapter.Date(2026, 3, 31)))
	if !slices.Contains(got, "XDR") {
		t.Error("missing XDR")
	}
	if slices.Contains(got, "SDR") {
		t.Error("SDR not normalized")
	}
}

func TestFetchNormalizesRatesByUnit(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 3, 31))
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Base == "JPY" })
	if i < 0 {
		t.Fatal("no JPY rate")
	}
	if rates[i].Rate >= 1 {
		t.Errorf("JPY rate = %v, want < 1", rates[i].Rate)
	}
}

func TestFetchNetNewCurrencies(t *testing.T) {
	got := bases(fetch(t, adapter.Date(2026, 3, 31)))
	for _, code := range []string{"BND", "EGP", "KHR", "MMK", "NPR"} {
		if !slices.Contains(got, code) {
			t.Errorf("missing %s", code)
		}
	}
}

func TestFetchRespectsUpto(t *testing.T) {
	upto := adapter.Date(2026, 3, 10)
	rates := fetch(t, upto)
	if slices.ContainsFunc(rates, func(r adapter.Rate) bool { return r.Date.After(upto) }) {
		t.Error("rates after upto")
	}
	if !slices.ContainsFunc(rates, func(r adapter.Rate) bool { return !r.Date.After(upto) }) {
		t.Error("no rates through upto")
	}
}

func TestParseMonthSingleRateAndUnit(t *testing.T) {
	rates, ok, err := parseMonth([]byte(`{"data":{"currency_code":"JPY","unit":100,
		"rate":{"date":"2026-03-02","middle_rate":2.5}}}`), "JPY", adapter.Date(2026, 3, 31))
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	want := adapter.Rate{Date: adapter.Date(2026, 3, 2), Base: "JPY", Quote: "MYR", Rate: 0.025}
	if len(rates) != 1 || rates[0] != want {
		t.Errorf("got %+v, want %+v", rates, want)
	}
}

func TestParseMonthSkipsEmptyData(t *testing.T) {
	for _, body := range []string{`{"data":[]}`, `{"data":null}`, `{}`} {
		if _, ok, err := parseMonth([]byte(body), "USD", adapter.Date(2026, 3, 31)); err != nil || ok {
			t.Errorf("%s: ok=%v err=%v, want skip", body, ok, err)
		}
	}
}

func TestParseMonthMissingRateFails(t *testing.T) {
	for _, body := range []string{
		`{"data":{"unit":1}}`,
		`{"data":{"unit":1,"rate":null}}`,
		`{"data":{"unit":1,"rate":[null]}}`,
	} {
		if _, _, err := parseMonth([]byte(body), "USD", adapter.Date(2026, 3, 31)); err == nil {
			t.Errorf("%s: want an error", body)
		}
	}
}

func TestParseMonthSkipsMissingMiddleRate(t *testing.T) {
	rates, ok, err := parseMonth([]byte(`{"data":{"unit":1,"rate":[{"date":"2026-03-02"},
		{"date":"2026-03-03","middle_rate":4.1}]}}`), "USD", adapter.Date(2026, 3, 31))
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if len(rates) != 1 || !rates[0].Date.Equal(adapter.Date(2026, 3, 3)) {
		t.Errorf("got %+v, want only 2026-03-03", rates)
	}
}

func TestFetchRequiresAfter(t *testing.T) {
	if _, err := New(nil).Fetch(context.Background(), time.Time{}, adapter.Date(2026, 3, 31)); err == nil {
		t.Error("want an error without after")
	}
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file string
		upto time.Time
	}{
		{"testdata/golden/fetch.json", adapter.Date(2026, 3, 31)},
		{"testdata/golden/upto.json", adapter.Date(2026, 3, 10)},
	} {
		t.Run(tc.file, func(t *testing.T) {
			g := golden.Load(t, tc.file)
			rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 3, 1), tc.upto)
			if err != nil {
				t.Fatal(err)
			}
			g.Check(t, rates)
		})
	}
}
