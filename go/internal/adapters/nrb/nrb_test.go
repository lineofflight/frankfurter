package nrb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "nrb", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 4, 1), adapter.Date(2026, 4, 5))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func find(rates []adapter.Rate, base string) adapter.Rate {
	for _, r := range rates {
		if r.Base == base {
			return r
		}
	}
	return adapter.Rate{}
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
	if n != 22 {
		t.Errorf("got %d rates on %s, want 22", n, first.Format("2006-01-02"))
	}
}

func TestParseRatesFromPayload(t *testing.T) {
	rates, err := parse([]byte(`[{"date": "2026-04-01", "rates": [
		{"currency": {"iso3": "USD", "name": "U.S. Dollar", "unit": 1}, "buy": "151.44", "sell": "152.04"},
		{"currency": {"iso3": "EUR", "name": "European Euro", "unit": 1}, "buy": "173.69", "sell": "174.38"}
	]}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want 2", len(rates))
	}
	r := rates[0]
	if r.Base != "USD" || r.Quote != "NPR" || !r.Date.Equal(adapter.Date(2026, 4, 1)) {
		t.Errorf("first rate = %+v", r)
	}
	if math.Abs(r.Rate-151.74) > 0.01 {
		t.Errorf("rate = %v, want 151.74", r.Rate)
	}
}

func TestParseSkipsMalformedDates(t *testing.T) {
	rates, err := parse([]byte(`[{"date": "2020-06.25",
		"rates": [{"currency": {"iso3": "USD", "unit": 1}, "buy": "133.5", "sell": "134.0"}]}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseNormalizesRatesByUnit(t *testing.T) {
	rates, err := parse([]byte(`[{"date": "2026-04-01", "rates": [
		{"currency": {"iso3": "JPY", "name": "Japanese Yen", "unit": 10}, "buy": "9.49", "sell": "9.53"},
		{"currency": {"iso3": "KRW", "name": "South Korean Won", "unit": 100}, "buy": "9.92", "sell": "9.96"}
	]}]`))
	if err != nil {
		t.Fatal(err)
	}
	// JPY: (9.49 + 9.53) / 2.0 / 10 = 0.951
	if got := find(rates, "JPY").Rate; math.Abs(got-0.951) > 0.001 {
		t.Errorf("JPY rate = %v, want 0.951", got)
	}
	// KRW: (9.92 + 9.96) / 2.0 / 100 = 0.0994
	if got := find(rates, "KRW").Rate; math.Abs(got-0.0994) > 0.0001 {
		t.Errorf("KRW rate = %v, want 0.0994", got)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 4, 1), adapter.Date(2026, 4, 5))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func respond(body string) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func TestFetchPagesThroughResults(t *testing.T) {
	var queries []url.Values
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.URL.Scheme + "://" + r.URL.Host + r.URL.Path; got != baseURL {
			t.Errorf("url = %s", got)
		}
		q := r.URL.Query()
		queries = append(queries, q)
		return respond(fmt.Sprintf(`{"data": {"payload": [{"date": "2026-04-0%s", "rates": [
			{"currency": {"iso3": "USD", "unit": 1}, "buy": "150", "sell": "152"}]}]},
			"pagination": {"page": %s, "pages": 2}}`, q.Get("page"), q.Get("page")))
	})}

	rates, err := New(client).Fetch(context.Background(), time.Time{}, adapter.Date(2026, 4, 5))
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 2 {
		t.Fatalf("made %d requests, want 2", len(queries))
	}
	for i, q := range queries {
		want := url.Values{"from": {""}, "to": {"2026-04-05"}, "page": {strconv.Itoa(i + 1)}, "per_page": {"100"}}
		if !reflect.DeepEqual(q, want) {
			t.Errorf("request %d query = %v, want %v", i+1, q, want)
		}
	}
	if len(rates) != 2 || !rates[1].Date.Equal(adapter.Date(2026, 4, 2)) || rates[0].Rate != 151 {
		t.Errorf("rates = %+v", rates)
	}
}

func TestFetchStopsWithoutPagination(t *testing.T) {
	n := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		n++
		return respond(`{"data": {"payload": []}, "pagination": null}`)
	})}
	if _, err := New(client).Fetch(context.Background(), adapter.Date(2026, 4, 1), adapter.Date(2026, 4, 5)); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("made %d requests, want 1", n)
	}
}

func TestParseRejectsNonArrayPayload(t *testing.T) {
	for _, payload := range []string{`null`, `"null"`, ``, `{"date": "2026-04-01"}`} {
		if _, err := parse([]byte(payload)); err == nil {
			t.Errorf("parse(%q): want error", payload)
		}
	}
}

func TestParseStringPayloadAndMixedTypes(t *testing.T) {
	inner := `[{"date": "2026-04-01", "rates": [
		{"currency": {"iso3": "JPY", "unit": "10"}, "buy": 9.49, "sell": "9.53"},
		{"currency": {"iso3": "INR"}, "buy": "160", "sell": "160.15"},
		{"currency": {"iso3": "USD", "unit": 1}, "buy": null, "sell": "152"},
		{"currency": {"iso3": "XXX", "unit": 1}, "buy": "0", "sell": "0"},
		{"currency": {"iso3": "YYY", "unit": 0}, "buy": "1", "sell": "2"}
	]}]`
	payload, _ := json.Marshal(inner)
	rates, err := parse(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want 2: %+v", len(rates), rates)
	}
	jpy := find(rates, "JPY")
	if jpy.Rate != 0.951 || jpy.Bid == nil || *jpy.Bid != 0.949 || jpy.Ask == nil || *jpy.Ask != 0.953 || jpy.Mid != nil {
		t.Errorf("JPY = %+v", jpy)
	}
	if inr := find(rates, "INR"); inr.Rate != 160.075 {
		t.Errorf("INR rate = %v, want 160.075", inr.Rate)
	}
}

func TestParseErrorsOnBadValues(t *testing.T) {
	for _, payload := range []string{
		`[{"rates": []}]`,
		`[{"date": "2026-04-01", "rates": [{"currency": {"iso3": "USD", "unit": "x"}, "buy": "1", "sell": "2"}]}]`,
		`[{"date": "2026-04-01", "rates": [{"currency": {"iso3": "USD", "unit": 1}, "buy": "", "sell": "2"}]}]`,
	} {
		if _, err := parse([]byte(payload)); err == nil {
			t.Errorf("parse(%s): want error", payload)
		}
	}
}
