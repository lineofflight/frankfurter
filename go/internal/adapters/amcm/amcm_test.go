package amcm

import (
	"context"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

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
	rates := fetch(t)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
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

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchRequestParamsAndWindow(t *testing.T) {
	var got string
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		got = r.URL.String()
		body := `{"message":"OK","data":[
			{"date":"2026-05-17 00:00:00","currency":"USD","unit":1.0,"usdMeanValue":8.07},
			{"date":"2026-05-18 00:00:00","currency":"USD","unit":1.0,"usdMeanValue":8.08},
			{"date":"2026-05-20 00:00:00","currency":"USD","unit":1.0,"usdMeanValue":8.09},
			{"date":"2026-05-21 00:00:00","currency":"USD","unit":1.0,"usdMeanValue":8.10}
		]}`
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	a := New(client)
	a.Now = func() time.Time { return time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC) }
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 5, 18), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	want := "https://www.amcm.gov.mo/api/v1.0/cms/financial_info?Begin=20260518&End=20260520&QueryType=1"
	if got != want {
		t.Errorf("url = %s, want %s", got, want)
	}
	if len(rates) != 2 || !rates[0].Date.Equal(adapter.Date(2026, 5, 18)) || !rates[1].Date.Equal(adapter.Date(2026, 5, 20)) {
		t.Errorf("rates = %+v, want 2026-05-18 and 2026-05-20 (after inclusive, today as end)", rates)
	}
}

func TestFetchKeepsATimeStampedRowDatedOnUpto(t *testing.T) {
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		body := `{"message":"OK","data":[
			{"date":"2012-05-11 14:00:00","currency":"USD","unit":1.0,"usdMeanValue":8.01},
			{"date":"2012-05-14 14:00:00","currency":"USD","unit":1.0,"usdMeanValue":8.02}
		]}`
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	rates, err := New(client).Fetch(context.Background(), adapter.Date(2012, 5, 11), adapter.Date(2012, 5, 14))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 || !rates[0].Date.Equal(adapter.Date(2012, 5, 11)) || !rates[1].Date.Equal(adapter.Date(2012, 5, 14)) {
		t.Errorf("rates = %+v, want 2012-05-11 and 2012-05-14 at midnight", rates)
	}
}

func TestParseStringValuesAndInvalidRows(t *testing.T) {
	rates := mustParse(t, `{"message":"OK","data":[
		{"date":"2026-05-22","currency":"KRW","unit":"100","usdMeanValue":"0.5412"},
		{"date":"2026-05-22 00:00:00","currency":"usd","unit":1.0,"usdMeanValue":8.07},
		{"date":"2026-05-22 00:00:00","currency":null,"unit":1.0,"usdMeanValue":8.07},
		{"date":null,"currency":"EUR","unit":1.0,"usdMeanValue":9.4},
		{"date":"2026-05-22 00:00:00","currency":"GBP","unit":null,"usdMeanValue":10.8},
		{"date":"2026-05-22 00:00:00","currency":"CHF","unit":1.0,"usdMeanValue":-1}
	]}`)
	if len(rates) != 1 || rates[0].Base != "KRW" || !rates[0].Date.Equal(adapter.Date(2026, 5, 22)) ||
		math.Abs(rates[0].Rate-0.005412) > 1e-12 {
		t.Errorf("rates = %+v, want only KRW 0.005412 on 2026-05-22", rates)
	}
}

func TestParseErrors(t *testing.T) {
	for name, body := range map[string]string{
		"not an object":  `[1, 2]`,
		"invalid JSON":   `<html>`,
		"missing data":   `{"message":"error"}`,
		"data not array": `{"message":"error","data":{}}`,
		"null data":      `{"message":"error","data":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parse([]byte(body)); err == nil {
				t.Error("want error")
			}
		})
	}
}
