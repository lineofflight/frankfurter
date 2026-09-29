package cba

import (
	"context"
	"io"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
)

func newAdapter(t *testing.T) *Adapter {
	t.Helper()
	a := New(vcrtest.Client(t, "cba", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	a.Now = func() time.Time { return time.Date(2026, 3, 19, 12, 0, 0, 0, time.UTC) }
	return a
}

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(2026, 3, 1), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchSinceDate(t *testing.T) {
	if rates := fetch(t); len(rates) == 0 {
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
		t.Errorf("got %d rates on %v, want several", n, first)
	}
}

// The golden replay matches on method and URI only, so this pins the SOAP
// envelopes (codes, date range) and actions.
func TestFetchSendsRecordedRequests(t *testing.T) {
	soapAction := func(r *http.Request, _ []byte, rec cassette.Request) bool {
		return r.Header.Get("SOAPAction") == rec.Headers.Get("SOAPAction")
	}
	a := New(vcrtest.Client(t, "cba", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI, vcrtest.Body, soapAction)))
	a.Now = func() time.Time { return time.Date(2026, 3, 19, 12, 0, 0, 0, time.UTC) }
	if _, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), time.Time{}); err != nil {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchRequestsYearLongChunks(t *testing.T) {
	var ranges []string
	dates := regexp.MustCompile(`<DateFrom>(.*)</DateFrom>\s*<DateTo>(.*)</DateTo>`)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		if m := dates.FindSubmatch(body); m != nil {
			ranges = append(ranges, string(m[1])+".."+string(m[2]))
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("<Envelope/>"))}, nil
	})}
	if _, err := New(client).Fetch(context.Background(), adapter.Date(2024, 1, 1), adapter.Date(2025, 1, 5)); err != nil {
		t.Fatal(err)
	}
	want := []string{"2024-01-01..2024-12-30", "2024-12-31..2025-01-05"}
	if !slices.Equal(ranges, want) {
		t.Errorf("ranges = %v, want %v", ranges, want)
	}
}

func TestFetchRequiresStartDate(t *testing.T) {
	if _, err := newAdapter(t).Fetch(context.Background(), time.Time{}, time.Time{}); err == nil {
		t.Fatal("want error for open start")
	}
}

func TestParseRangeConvertsMetalsAndAmounts(t *testing.T) {
	data := []byte(`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>
<ExchangeRatesByDateRangeByISOResponse xmlns="http://www.cba.am/"><ExchangeRatesByDateRangeByISOResult>
<diffgr:diffgram xmlns:diffgr="urn:schemas-microsoft-com:xml-diffgram-v1"><DocumentElement xmlns="">
<ExchangeRatesByRange><Rate>10</Rate><Amount>100</Amount><ISO>JPY</ISO><RateDate>2026-03-02T00:00:00</RateDate></ExchangeRatesByRange>
<ExchangeRatesByRange><Rate>2</Rate><Amount>1</Amount><ISO>XAU</ISO><RateDate>2026-03-02T00:00:00</RateDate></ExchangeRatesByRange>
<ExchangeRatesByRange><Rate>2</Rate><Amount>1</Amount><RateDate>2026-03-02T00:00:00</RateDate></ExchangeRatesByRange>
</DocumentElement></diffgr:diffgram></ExchangeRatesByDateRangeByISOResult></ExchangeRatesByDateRangeByISOResponse>
</soap:Body></soap:Envelope>`)
	rates, err := parseRange(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 {
		t.Fatalf("got %d rates, want 2", len(rates))
	}
	if !rates[0].Date.Equal(adapter.Date(2026, 3, 2)) || rates[0].Quote != "AMD" || rates[0].Rate != 0.1 {
		t.Errorf("JPY = %+v", rates[0])
	}
	if math.Abs(rates[1].Rate-2*troyOunceGrams) > 1e-12 {
		t.Errorf("XAU = %v", rates[1].Rate)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	a.Now = g.Now(t)
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
