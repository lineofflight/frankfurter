package nbp

import (
	"context"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "nbp", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 5))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchSinceDate(t *testing.T) {
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
		t.Errorf("got %d rates on %s, want more than 1", n, first)
	}
}

func TestFetchIncludesTableBCurrencies(t *testing.T) {
	for _, r := range fetch(t) {
		if r.Base == "ALL" {
			return
		}
	}
	t.Error("no ALL rate")
}

func TestFetchXAUAgainstPLN(t *testing.T) {
	n := 0
	for _, r := range fetch(t) {
		if r.Base != "XAU" {
			continue
		}
		n++
		if r.Quote != "PLN" {
			t.Errorf("XAU quote = %s, want PLN", r.Quote)
		}
	}
	if n == 0 {
		t.Error("no XAU rates")
	}
}

func TestParseGoldNormalizesToTroyOunce(t *testing.T) {
	rates, err := parseGold([]byte(`[{"data":"2026-04-25","cena":407.18}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "XAU" || r.Quote != "PLN" || !r.Date.Equal(adapter.Date(2026, 4, 25)) {
		t.Errorf("got %+v", r)
	}
	if want := 407.18 * adapter.GramsPerTroyOunce; math.Abs(r.Rate-want) > 0.0001 {
		t.Errorf("rate = %v, want %v", r.Rate, want)
	}
}

func TestParseRelabelsRetiredTableBCodesThatCarryCurrentValues(t *testing.T) {
	rates, err := parse([]byte(`[
		{"table":"B","no":"5/B/NBP/2002","effectiveDate":"2002-02-26","rates":[
			{"currency":"rubel","code":"BYB","mid":0.002494}
		]},
		{"table":"B","no":"27/B/NBP/2002","effectiveDate":"2002-12-24","rates":[
			{"currency":"afgani","code":"AFA","mid":0.000816}
		]},
		{"table":"B","no":"1/B/NBP/2003","effectiveDate":"2003-01-07","rates":[
			{"currency":"afgani","code":"AFA","mid":0.089056}
		]},
		{"table":"B","no":"22/B/NBP/2003","effectiveDate":"2003-10-28","rates":[
			{"currency":"kwanza","code":"AON","mid":0.0503}
		]}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	var values []float64
	for _, r := range rates {
		got = append(got, r.Date.Format(time.DateOnly)+" "+r.Base)
		values = append(values, r.Rate)
	}
	want := []string{"2002-02-26 BYR", "2002-12-24 AFA", "2003-01-07 AFN", "2003-10-28 AOA"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if want := []float64{0.002494, 0.000816, 0.089056, 0.0503}; !slices.Equal(values, want) {
		t.Errorf("rates = %v, want %v", values, want)
	}
}

func TestParseRelabels2009ZimbabweDollarPublishedUnderZWR(t *testing.T) {
	rates, err := parse([]byte(`[
		{"table":"B","no":"5/B/NBP/2009","effectiveDate":"2009-02-04","rates":[
			{"currency":"dolar Zimbabwe","code":"ZWR","mid":1.0e-08}
		]},
		{"table":"B","no":"8/B/NBP/2009","effectiveDate":"2009-02-25","rates":[
			{"currency":"dolar Zimbabwe","code":"ZWR","mid":0.043749}
		]}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	var values []float64
	for _, r := range rates {
		got = append(got, r.Date.Format(time.DateOnly)+" "+r.Base)
		values = append(values, r.Rate)
	}
	if want := []string{"2009-02-04 ZWR", "2009-02-25 ZWL"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if want := []float64{1.0e-08, 0.043749}; !slices.Equal(values, want) {
		t.Errorf("rates = %v, want %v", values, want)
	}
}

// stub answers every request with status and an empty body, recording the URLs,
// as the Ruby spec's WebMock stub does.
type stub struct {
	status int
	urls   []string
}

func (s *stub) RoundTrip(req *http.Request) (*http.Response, error) {
	s.urls = append(s.urls, req.URL.String())
	return &http.Response{
		StatusCode: s.status,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

func TestFetchTreats404AsNoData(t *testing.T) {
	rt := &stub{status: http.StatusNotFound}
	a := New(&http.Client{Transport: rt})
	today := a.Today()
	rates, err := a.Fetch(context.Background(), today.AddDate(0, 0, -3), today)
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
	if len(rt.urls) != 3 {
		t.Errorf("made %d requests, want 3", len(rt.urls))
	}
	for _, u := range rt.urls {
		if !strings.HasPrefix(u, "https://api.nbp.pl/") {
			t.Errorf("requested %s, want api.nbp.pl", u)
		}
	}
}

func TestFetchSplitsIntoNinetyThreeDayChunks(t *testing.T) {
	rt := &stub{status: http.StatusNotFound}
	a := New(&http.Client{Transport: rt})
	if _, err := a.Fetch(context.Background(), adapter.Date(2026, 1, 1), adapter.Date(2026, 6, 30)); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"https://api.nbp.pl/api/exchangerates/tables/A/2026-01-01/2026-04-03/?format=json",
		"https://api.nbp.pl/api/exchangerates/tables/B/2026-01-01/2026-04-03/?format=json",
		"https://api.nbp.pl/api/cenyzlota/2026-01-01/2026-04-03/?format=json",
		"https://api.nbp.pl/api/exchangerates/tables/A/2026-04-04/2026-06-30/?format=json",
		"https://api.nbp.pl/api/exchangerates/tables/B/2026-04-04/2026-06-30/?format=json",
		"https://api.nbp.pl/api/cenyzlota/2026-04-04/2026-06-30/?format=json",
	}
	if !slices.Equal(rt.urls, want) {
		t.Errorf("requested\n%s\nwant\n%s", strings.Join(rt.urls, "\n"), strings.Join(want, "\n"))
	}
}

func TestFetchFailsOnOtherErrors(t *testing.T) {
	a := New(&http.Client{Transport: &stub{status: http.StatusForbidden}})
	if _, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 5)); err == nil {
		t.Error("want an error for HTTP 403")
	}
}

func TestParseSkipsNonISOAndZeroRates(t *testing.T) {
	rates, err := parse([]byte(`[{"effectiveDate":"2026-03-02","rates":[
		{"code":"USD","mid":3.9},{"code":"XDR1","mid":5.1},{"code":"eur","mid":4.2},
		{"code":"ZWL","mid":0},{"code":"VES","mid":null},{"code":"CHF"}]}]`))
	if err != nil {
		t.Fatal(err)
	}
	want := []adapter.Rate{{Date: adapter.Date(2026, 3, 2), Base: "USD", Quote: "PLN", Rate: 3.9}}
	if !slices.Equal(rates, want) {
		t.Errorf("got %+v, want %+v", rates, want)
	}
}

func TestParseGoldSkipsZeroAndMissingPrices(t *testing.T) {
	rates, err := parseGold([]byte(`[{"data":"2026-04-24","cena":0},{"data":"2026-04-25","cena":null},{"data":"2026-04-26"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %+v, want none", rates)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), adapter.Date(2026, 3, 5))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
