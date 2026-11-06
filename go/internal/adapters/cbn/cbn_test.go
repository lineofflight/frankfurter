package cbn

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
	a := New(vcrtest.Client(t, "cbn", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func fetchWeek(t *testing.T) []adapter.Rate {
	return fetch(t, adapter.Date(2026, 5, 14), adapter.Date(2026, 5, 21))
}

func bases(rates []adapter.Rate) []string {
	var out []string
	for _, r := range rates {
		out = append(out, r.Base)
	}
	return out
}

func mustParse(t *testing.T, json string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(json))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchFiltersByDateRange(t *testing.T) {
	rates := fetchWeek(t)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if !r.Date.After(adapter.Date(2026, 5, 14)) || r.Date.After(adapter.Date(2026, 5, 21)) {
			t.Errorf("date %s outside (2026-05-14, 2026-05-21]", r.Date.Format(time.DateOnly))
		}
	}
}

func TestFetchStampsNGNAsQuote(t *testing.T) {
	rates := fetchWeek(t)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if r.Quote != "NGN" {
			t.Errorf("quote = %q, want NGN", r.Quote)
		}
	}
}

func TestFetchReturnsMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetchWeek(t)
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
	if n <= 5 {
		t.Errorf("got %d currencies on %s, want more than 5", n, first.Format(time.DateOnly))
	}
}

func TestFetchExcludesWAUAButMapsSDRToXDR(t *testing.T) {
	got := bases(fetchWeek(t))
	if slices.Contains(got, "WAUA") || slices.Contains(got, "SDR") {
		t.Errorf("bases %v include WAUA or SDR", got)
	}
	if !slices.Contains(got, "XDR") {
		t.Errorf("bases %v lack XDR", got)
	}
}

func TestFetchUSDInPlausibleRange(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 5, 20), adapter.Date(2026, 5, 21))
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool {
		return r.Base == "USD" && r.Date.Equal(adapter.Date(2026, 5, 21))
	})
	if i < 0 {
		t.Fatal("no USD rate on 2026-05-21")
	}
	if math.Abs(rates[i].Rate-1372) > 50 {
		t.Errorf("USD/NGN = %v, want 1372 +/- 50", rates[i].Rate)
	}
}

func TestParseCentralRateAsMid(t *testing.T) {
	rates := mustParse(t, `[
		{"id":1,"currency":"US DOLLAR","ratedate":"2026-05-21","buyingrate":"1371.3079","centralrate":"1371.8079","sellingrate":"1372.3079"}
	]`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	want := adapter.Rate{Date: adapter.Date(2026, 5, 21), Base: "USD", Quote: "NGN", Rate: 1371.8079}
	if rates[0] != want {
		t.Errorf("rate = %+v, want %+v", rates[0], want)
	}
}

func TestParseMapsNameVariants(t *testing.T) {
	rates := mustParse(t, `[
		{"id":1,"currency":"YEN","ratedate":"2026-05-21","centralrate":"8.6147"},
		{"id":2,"currency":"JAPANESE YEN","ratedate":"2012-08-16","centralrate":"1.9589"},
		{"id":3,"currency":"POUND STERLING","ratedate":"2012-08-28","centralrate":"245.3740"},
		{"id":4,"currency":"POUNDS STERLING","ratedate":"2026-05-21","centralrate":"1839.5944"},
		{"id":5,"currency":"DANISH KRONA","ratedate":"2026-05-21","centralrate":"212.7461"},
		{"id":6,"currency":"DANISH KRONER","ratedate":"2012-08-28","centralrate":"26.1615"},
		{"id":7,"currency":"YUAN/RENMINBI","ratedate":"2026-05-21","centralrate":"201.5942"},
		{"id":8,"currency":"CFA","ratedate":"2026-05-21","centralrate":"2.4200"}
	]`)
	got := bases(rates)
	for _, code := range []string{"JPY", "GBP", "DKK", "CNY", "XOF"} {
		if !slices.Contains(got, code) {
			t.Errorf("bases %v lack %s", got, code)
		}
	}
	for _, code := range []string{"JPY", "GBP", "DKK"} {
		n := 0
		for _, b := range got {
			if b == code {
				n++
			}
		}
		if n != 2 {
			t.Errorf("got %d %s rows, want 2", n, code)
		}
	}
}

func TestParseToleratesWhitespaceInNames(t *testing.T) {
	rates := mustParse(t, `[
		{"id":1,"currency":"EURO ","ratedate":"2026-05-21","centralrate":"1590.2"},
		{"id":2,"currency":"SWISS FRANC\t","ratedate":"2026-05-21","centralrate":"1737.5"}
	]`)
	if got := bases(rates); !slices.Equal(got, []string{"EUR", "CHF"}) {
		t.Errorf("bases = %v, want [EUR CHF]", got)
	}
}

func TestParseExcludesWAUAAndUnknownNames(t *testing.T) {
	rates := mustParse(t, `[
		{"id":1,"currency":"WAUA","ratedate":"2026-05-21","centralrate":"1876.66"},
		{"id":2,"currency":"SDR","ratedate":"2026-05-21","centralrate":"1884.04"},
		{"id":3,"currency":"NAIRA","ratedate":"2012-05-31","centralrate":"155.25"},
		{"id":4,"currency":"POESO","ratedate":"2009-07-01","centralrate":"27.62"},
		{"id":5,"currency":"US DOLLAR","ratedate":"2026-05-21","centralrate":"1371.81"}
	]`)
	got := bases(rates)
	slices.Sort(got)
	if !slices.Equal(got, []string{"USD", "XDR"}) {
		t.Errorf("bases = %v, want [USD XDR]", got)
	}
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Base == "XDR" })
	if i < 0 || rates[i].Rate != 1884.04 {
		t.Errorf("XDR rate wrong: %+v", rates)
	}
}

func TestParseSkipsMissingOrZeroCentralRate(t *testing.T) {
	rates := mustParse(t, `[
		{"id":1,"currency":"US DOLLAR","ratedate":"2026-05-21","centralrate":null},
		{"id":2,"currency":"EURO","ratedate":"2026-05-21","centralrate":""},
		{"id":3,"currency":"YEN","ratedate":"2026-05-21","centralrate":"0"},
		{"id":4,"currency":"SWISS FRANC","ratedate":"2026-05-21","centralrate":"1737.5"}
	]`)
	if len(rates) != 1 || rates[0].Base != "CHF" {
		t.Errorf("rates = %+v, want one CHF row", rates)
	}
}

func TestParseKeepsNBSPPaddedNamesUnmapped(t *testing.T) {
	rates := mustParse(t, `[
		{"id":1,"currency":"EURO ","ratedate":"2026-05-21","centralrate":"1590.2"},
		{"id":2,"currency":"\u0000SWISS FRANC\u000b","ratedate":"2026-05-21","centralrate":"1737.5"}
	]`)
	if got := bases(rates); !slices.Equal(got, []string{"CHF"}) {
		t.Errorf("bases = %v, want [CHF]", got)
	}
}

func TestParseRejectsNonNumericCentralRate(t *testing.T) {
	if _, err := parse([]byte(`[{"currency":"EURO","ratedate":"2026-05-21","centralrate":"n/a"}]`)); err == nil {
		t.Error("want an error for a non-numeric centralrate")
	}
}

func TestParseAcceptsNumericCentralRateAndTimestamp(t *testing.T) {
	rates := mustParse(t, `[{"currency":"EURO","ratedate":"2026-05-21T00:00:00","centralrate":1590.25}]`)
	want := []adapter.Rate{{Date: adapter.Date(2026, 5, 21), Base: "EUR", Quote: "NGN", Rate: 1590.25}}
	if !slices.Equal(rates, want) {
		t.Errorf("rates = %+v, want %+v", rates, want)
	}
}

func TestParseRejectsNonArray(t *testing.T) {
	if _, err := parse([]byte(`{"error":true}`)); err == nil {
		t.Error("want an error for a JSON object")
	}
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file        string
		after, upto time.Time
	}{
		{"testdata/golden/fetch.json", adapter.Date(2026, 5, 14), adapter.Date(2026, 5, 21)},
		{"testdata/golden/fetch_usd.json", adapter.Date(2026, 5, 20), adapter.Date(2026, 5, 21)},
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
