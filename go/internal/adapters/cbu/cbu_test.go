package cbu

import (
	"context"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "cbu", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 4, 1), adapter.Date(2026, 4, 3))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func mustParse(t *testing.T, s string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(s))
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

func TestParse(t *testing.T) {
	rates := mustParse(t, `[
		{"id":69,"Code":"840","Ccy":"USD","CcyNm_EN":"US Dollar","Nominal":"1","Rate":"12194.21","Diff":"-16.5","Date":"01.04.2026"},
		{"id":21,"Code":"978","Ccy":"EUR","CcyNm_EN":"Euro","Nominal":"1","Rate":"13984.32","Diff":"-51.89","Date":"01.04.2026"}
	]`)
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want 2", len(rates))
	}
	if rates[0].Base != "USD" || rates[0].Quote != "UZS" {
		t.Errorf("first rate = %+v, want USD/UZS", rates[0])
	}
	if math.Abs(rates[0].Rate-12194.21) > 0.01 {
		t.Errorf("rate = %v, want 12194.21", rates[0].Rate)
	}
}

func TestParseNormalizesByNominal(t *testing.T) {
	rates := mustParse(t, `[{"id":1,"Code":"360","Ccy":"IDR","CcyNm_EN":"Indonesian Rupiah","Nominal":"10","Rate":"7.18","Diff":"0","Date":"01.04.2026"}]`)
	if len(rates) != 1 || math.Abs(rates[0].Rate-0.718) > 0.001 {
		t.Errorf("got %+v, want one rate of 0.718", rates)
	}
}

func TestParseSkipsZeroRates(t *testing.T) {
	rates := mustParse(t, `[{"id":1,"Code":"840","Ccy":"USD","CcyNm_EN":"US Dollar","Nominal":"1","Rate":"0","Diff":"0","Date":"01.04.2026"}]`)
	if len(rates) != 0 {
		t.Errorf("got %+v, want none", rates)
	}
}

func TestParseRestoresOldRubleBeforeFirstNewRubleBulletin(t *testing.T) {
	rates := mustParse(t, `[
		{"id":1,"Code":"810","Ccy":"RUB","CcyNm_EN":"Russian Ruble","Nominal":"1000","Rate":"13.46","Diff":"0","Date":"30.12.1997"},
		{"id":1,"Code":"643","Ccy":"RUB","CcyNm_EN":"Russian Ruble","Nominal":"1","Rate":"13.48","Diff":"0","Date":"06.01.1998"}
	]`)
	if len(rates) != 2 || rates[0].Base != "RUR" || rates[1].Base != "RUB" {
		t.Fatalf("got %+v, want bases RUR, RUB", rates)
	}
	if math.Abs(rates[0].Rate-0.01346) > 1e-9 {
		t.Errorf("rate = %v, want 0.01346", rates[0].Rate)
	}
}

func TestParseSkipsInvalidCurrencyCodes(t *testing.T) {
	rates := mustParse(t, `[{"id":1,"Code":"999","Ccy":"XX","CcyNm_EN":"Invalid","Nominal":"1","Rate":"1.5","Diff":"0","Date":"01.04.2026"}]`)
	if len(rates) != 0 {
		t.Errorf("got %+v, want none", rates)
	}
}

func TestParseHandlesEmptyResponse(t *testing.T) {
	if rates := mustParse(t, "[]"); len(rates) != 0 {
		t.Errorf("got %+v, want none", rates)
	}
}

func TestParseRejectsNonArray(t *testing.T) {
	if _, err := parse([]byte(`{"error": true}`)); err == nil {
		t.Error("want an error for a JSON object")
	}
}

func TestParseRejectsUnparseableValues(t *testing.T) {
	for _, tc := range []struct{ nominal, rate string }{
		{`"1.5"`, `"1"`},
		{`""`, `"1"`},
		{`null`, `"1"`},
		{`"1"`, `"abc"`},
		{`"1"`, `"NaN"`},
		{`"1"`, `"Infinity"`},
		{`"1"`, `null`},
	} {
		s := `[{"Ccy":"USD","Nominal":` + tc.nominal + `,"Rate":` + tc.rate + `,"Date":"01.04.2026"}]`
		if _, err := parse([]byte(s)); err == nil {
			t.Errorf("Nominal %s, Rate %s: want an error", tc.nominal, tc.rate)
		}
	}
}

func TestParseReadsValuesLikeRuby(t *testing.T) {
	rates := mustParse(t, `[
		{"Ccy":"USD","Nominal":1,"Rate":12194.21,"Date":"01.04.2026"},
		{"Ccy":"IDR","Nominal":"010","Rate":"8","Date":"01.04.2026"},
		{"Ccy":"JPY","Nominal":"0","Rate":"8","Date":"01.04.2026"}
	]`)
	if len(rates) != 2 {
		t.Fatalf("got %+v, want 2 rates", rates)
	}
	if rates[0].Rate != 12194.21 {
		t.Errorf("bare numbers: rate = %v, want 12194.21", rates[0].Rate)
	}
	if rates[1].Rate != 1 {
		t.Errorf("octal nominal: rate = %v, want 8/8", rates[1].Rate)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchRequestsWeekdaysInclusive(t *testing.T) {
	var urls []string
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		urls = append(urls, r.URL.String())
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("[]")), Header: http.Header{}, Request: r}, nil
	})}
	// Friday 2026-04-03 through Monday 2026-04-06.
	if _, err := New(client).Fetch(context.Background(), adapter.Date(2026, 4, 3), adapter.Date(2026, 4, 6)); err != nil {
		t.Fatal(err)
	}
	want := "https://cbu.uz/en/arkhiv-kursov-valyut/json/all/2026-04-03/ https://cbu.uz/en/arkhiv-kursov-valyut/json/all/2026-04-06/"
	if got := strings.Join(urls, " "); got != want {
		t.Errorf("requested %s, want %s", got, want)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 4, 1), adapter.Date(2026, 4, 3))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
