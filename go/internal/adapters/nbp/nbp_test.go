package nbp

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

// notFound answers every request with a 404, as the Ruby spec's WebMock stub does.
type notFound struct{ hosts []string }

func (n *notFound) RoundTrip(req *http.Request) (*http.Response, error) {
	n.hosts = append(n.hosts, req.URL.Host)
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

func TestFetchTreats404AsNoData(t *testing.T) {
	rt := &notFound{}
	a := New(&http.Client{Transport: rt})
	today := a.Today()
	rates, err := a.Fetch(context.Background(), today.AddDate(0, 0, -3), today)
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
	if len(rt.hosts) != 3 {
		t.Errorf("made %d requests, want 3", len(rt.hosts))
	}
	for _, h := range rt.hosts {
		if h != "api.nbp.pl" {
			t.Errorf("requested host %s, want api.nbp.pl", h)
		}
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
