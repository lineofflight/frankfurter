package hnb

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "hnb", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host, vcrtest.Path)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2023, 1, 2), adapter.Date(2023, 1, 6))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchRates(t *testing.T) {
	rates := fetch(t)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	if rates[0].Base != "EUR" {
		t.Errorf("base = %q, want EUR", rates[0].Base)
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
		t.Errorf("got %d rates on %v, want several", n, first)
	}
}

func TestParseCommaDecimals(t *testing.T) {
	rates, err := parse([]byte(`[{"datum_primjene":"2023-01-02","valuta":"USD","srednji_tecaj":"1,066200"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "EUR" || r.Quote != "USD" || !r.Date.Equal(adapter.Date(2023, 1, 2)) {
		t.Errorf("rate = %+v", r)
	}
	if math.Abs(r.Rate-1.0662) > 0.0001 {
		t.Errorf("rate = %v, want 1.0662", r.Rate)
	}
}

func TestParseSkipsZeroRates(t *testing.T) {
	rates, err := parse([]byte(`[{"datum_primjene":"2023-01-02","valuta":"USD","srednji_tecaj":"0,000000"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2023, 1, 2), adapter.Date(2023, 1, 6))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchQueryParams(t *testing.T) {
	tests := []struct {
		name        string
		after, upto time.Time
		want        string
	}{
		{"none", time.Time{}, time.Time{}, ""},
		{"both", adapter.Date(2023, 1, 2), adapter.Date(2023, 1, 6), "datum-primjene-do=2023-01-06&datum-primjene-od=2023-01-02"},
		{"after only", adapter.Date(2023, 1, 2), time.Time{}, "datum-primjene-do=2024-05-10&datum-primjene-od=2023-01-02"},
		{"upto only", time.Time{}, adapter.Date(2023, 1, 6), "datum-primjene-do=2023-01-06"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got *url.URL
			a := New(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				got = r.URL
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("[]")), Header: http.Header{}}, nil
			})})
			a.Now = func() time.Time { return time.Date(2024, 5, 10, 12, 0, 0, 0, time.Local) }
			if _, err := a.Fetch(context.Background(), tt.after, tt.upto); err != nil {
				t.Fatal(err)
			}
			if got.Host != "api.hnb.hr" || got.Path != "/tecajn-eur/v3" {
				t.Errorf("url = %v", got)
			}
			if got.Query().Encode() != tt.want {
				t.Errorf("query = %q, want %q", got.Query().Encode(), tt.want)
			}
		})
	}
}

func TestParseSkipsMissingFields(t *testing.T) {
	rates, err := parse([]byte(`[
		{"valuta":"USD","srednji_tecaj":"1,0662"},
		{"datum_primjene":"2023-01-02","srednji_tecaj":"1,0662"},
		{"datum_primjene":"2023-01-02","valuta":"USD","srednji_tecaj":null},
		{"datum_primjene":"2023-01-02","valuta":"GBP","srednji_tecaj":" 0,88 "}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 || rates[0].Quote != "GBP" || rates[0].Rate != 0.88 {
		t.Errorf("rates = %+v", rates)
	}
}

func TestParseRejectsBadRates(t *testing.T) {
	for _, v := range []string{"", "n/a", "NaN", "Inf"} {
		_, err := parse([]byte(`[{"datum_primjene":"2023-01-02","valuta":"USD","srednji_tecaj":"` + v + `"}]`))
		if err == nil {
			t.Errorf("%q: want error", v)
		}
	}
}
