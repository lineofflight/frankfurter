package amcm

import (
	"context"
	"math"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "amcm", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 5, 18), adapter.Date(2026, 5, 22))
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

func TestFetchUsesMOPAsQuote(t *testing.T) {
	for _, r := range fetch(t) {
		if r.Quote != "MOP" {
			t.Fatalf("quote = %q, want MOP", r.Quote)
		}
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
		t.Errorf("got %d rates on %s, want more than 1", n, first)
	}
}

func TestParse(t *testing.T) {
	rates := mustParse(t, `{"message":"OK","data":[
		{"id":1,"date":"2026-05-22 00:00:00","currency":"USD","unit":1.0,"usdMean":"8.0705","usdMeanValue":8.07050000,"bid":"7.8349"}
	]}`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	want := adapter.Rate{Date: adapter.Date(2026, 5, 22), Base: "USD", Quote: "MOP", Rate: 8.0705}
	if rates[0] != want {
		t.Errorf("rate = %+v, want %+v", rates[0], want)
	}
}

func TestParseDividesByUnit(t *testing.T) {
	rates := mustParse(t, `{"message":"OK","data":[
		{"id":1,"date":"2026-05-22 00:00:00","currency":"JPY","unit":100.0,"usdMean":"5.0727","usdMeanValue":5.07270000,"bid":"159.09"}
	]}`)
	if rates[0].Base != "JPY" {
		t.Errorf("base = %q, want JPY", rates[0].Base)
	}
	if math.Abs(rates[0].Rate-0.050727) > 0.000001 {
		t.Errorf("rate = %v, want 0.050727", rates[0].Rate)
	}
}

func TestParseAliasesECUToXEU(t *testing.T) {
	rates := mustParse(t, `{"message":"OK","data":[
		{"id":1,"date":"1995-01-03 00:00:00","currency":"ECU","unit":1.0,"usdMean":"10.1234","usdMeanValue":10.12340000,"bid":"0"}
	]}`)
	if rates[0].Base != "XEU" {
		t.Errorf("base = %q, want XEU", rates[0].Base)
	}
}

func TestParseSkips(t *testing.T) {
	for name, json := range map[string]string{
		"non-currency LIQ entries": `{"message":"OK","data":[
			{"id":1,"date":"2026-05-22 00:00:00","currency":"LIQ","unit":0.0,"usdMean":"0.0000","usdMeanValue":0.0,"bid":"1.85"}
		]}`,
		"non-positive rates": `{"message":"OK","data":[
			{"id":1,"date":"2026-05-22 00:00:00","currency":"USD","unit":1.0,"usdMean":"0.0000","usdMeanValue":0.0,"bid":"0"}
		]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if rates := mustParse(t, json); len(rates) != 0 {
				t.Errorf("got %d rates, want none", len(rates))
			}
		})
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 5, 18), adapter.Date(2026, 5, 22))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
