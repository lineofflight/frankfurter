package nbrb

import (
	"context"
	"io"
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
	a := New(vcrtest.Client(t, "nbrb", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	// The cassette ends on 2026-03-17, the day it was recorded.
	a.Now = func() time.Time { return time.Date(2026, 3, 17, 12, 0, 0, 0, time.UTC) }
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 1), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchesRatesSinceDate(t *testing.T) {
	if len(fetch(t)) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchesMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t)
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
	if n <= 1 {
		t.Errorf("got %d rates on %s, want more than 1", n, first.Format(time.DateOnly))
	}
}

func TestParseDynamicsSkipsWeekendsAndScales(t *testing.T) {
	rates, err := parseDynamics([]byte(`[
		{"Cur_ID":510,"Date":"2026-03-06T00:00:00","Cur_OfficialRate":7.8014},
		{"Cur_ID":510,"Date":"2026-03-07T00:00:00","Cur_OfficialRate":7.9}
	]`), currency{ID: 510, ISO: "AMD", Scale: 1000})
	if err != nil {
		t.Fatal(err)
	}
	want := adapter.Rate{Date: adapter.Date(2026, 3, 6), Base: "AMD", Quote: "BYN", Rate: 7.8014 / 1000}
	if len(rates) != 1 || rates[0] != want {
		t.Errorf("got %+v, want [%+v]", rates, want)
	}
}

func TestParseDynamicsRejectsNonArray(t *testing.T) {
	if _, err := parseDynamics([]byte(`{"error":true}`), currency{}); err == nil {
		t.Error("want an error for a JSON object")
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

type recorder struct{ urls []string }

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.urls = append(r.urls, req.URL.String())
	body := `[]`
	if req.URL.Path == "/exrates/rates" {
		body = `[{"Cur_ID":431,"Cur_Abbreviation":"USD","Cur_Scale":1}]`
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: req}, nil
}

func TestFetchChunksDynamicsByYear(t *testing.T) {
	rec := &recorder{}
	a := New(&http.Client{Transport: rec})
	if _, err := a.Fetch(context.Background(), adapter.Date(2024, 1, 1), adapter.Date(2025, 2, 1)); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"https://api.nbrb.by/exrates/rates?periodicity=0",
		"https://api.nbrb.by/exrates/rates/dynamics/431?endDate=2024-12-30&startDate=2024-01-01",
		"https://api.nbrb.by/exrates/rates/dynamics/431?endDate=2025-02-01&startDate=2024-12-31",
	}
	if !slices.Equal(rec.urls, want) {
		t.Errorf("got %q, want %q", rec.urls, want)
	}
}

func TestFetchRequiresStartDate(t *testing.T) {
	if _, err := New(&http.Client{Transport: &recorder{}}).Fetch(context.Background(), time.Time{}, time.Time{}); err == nil {
		t.Error("want an error without a start date")
	}
}
