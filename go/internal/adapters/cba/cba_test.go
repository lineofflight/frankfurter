package cba

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
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
