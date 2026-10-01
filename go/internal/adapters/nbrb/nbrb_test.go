package nbrb

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

func fetch(t *testing.T, after, upto time.Time) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "nbrb", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func fetchRecent(t *testing.T) []adapter.Rate {
	t.Helper()
	return fetch(t, adapter.Date(2026, 3, 2), adapter.Date(2026, 3, 6))
}

func fetchRenumbering(t *testing.T) []adapter.Rate {
	t.Helper()
	return fetch(t, adapter.Date(2021, 7, 5), adapter.Date(2021, 7, 13))
}

// series maps each date of base's rows to its rate.
func series(rates []adapter.Rate, base string) map[time.Time]float64 {
	out := map[time.Time]float64{}
	for _, r := range rates {
		if r.Base == base {
			out[r.Date] = r.Rate
		}
	}
	return out
}

func TestFetchesRatesSinceDate(t *testing.T) {
	if len(fetchRecent(t)) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchesMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetchRecent(t)
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

func TestFetchReachesBackPastThe2021Renumbering(t *testing.T) {
	usd := series(fetchRenumbering(t), "USD")

	var dates []time.Time
	for d := range usd {
		dates = append(dates, d)
	}
	slices.SortFunc(dates, time.Time.Compare)
	var want []time.Time
	for _, day := range []int{5, 6, 7, 8, 9, 12, 13} {
		want = append(want, adapter.Date(2021, 7, day))
	}
	if !slices.EqualFunc(dates, want, time.Time.Equal) {
		t.Errorf("USD dates = %v, want %v", dates, want)
	}
	if got := usd[adapter.Date(2021, 7, 8)]; got != 2.5552 {
		t.Errorf("USD on 2021-07-08 = %v, want 2.5552", got)
	}
	if got := usd[adapter.Date(2021, 7, 9)]; got != 2.5921 {
		t.Errorf("USD on 2021-07-09 = %v, want 2.5921", got)
	}
}

func TestFetchAppliesTheScaleOfEachID(t *testing.T) {
	jpy := series(fetchRenumbering(t), "JPY")

	for date, want := range map[time.Time]float64{
		adapter.Date(2021, 7, 8): 0.023071,
		adapter.Date(2021, 7, 9): 0.023585,
	} {
		if got := jpy[date]; math.Abs(got-want) > 1e-9 {
			t.Errorf("JPY on %s = %v, want %v", date.Format(time.DateOnly), got, want)
		}
	}
}

func TestFetchSkipsMonthlyIDs(t *testing.T) {
	rates := fetchRenumbering(t)

	var first time.Time
	for d := range series(rates, "AMD") {
		if first.IsZero() || d.Before(first) {
			first = d
		}
	}
	if !first.Equal(adapter.Date(2021, 7, 9)) {
		t.Errorf("first AMD date = %s, want 2021-07-09", first.Format(time.DateOnly))
	}
	if len(series(rates, "BRL")) > 0 {
		t.Error("want no BRL rows")
	}
}

func TestFetchStartsAtTheBYNRedenomination(t *testing.T) {
	rates := fetch(t, adapter.Date(2016, 6, 28), adapter.Date(2016, 7, 5))

	first := slices.MinFunc(rates, func(a, b adapter.Rate) int { return a.Date.Compare(b.Date) }).Date
	if !first.Equal(adapter.Date(2016, 7, 1)) {
		t.Errorf("first date = %s, want 2016-07-01", first.Format(time.DateOnly))
	}
	if got := series(rates, "USD")[adapter.Date(2016, 7, 1)]; got != 2.0053 {
		t.Errorf("USD on 2016-07-01 = %v, want 2.0053", got)
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
	for _, tc := range []struct {
		file        string
		after, upto time.Time
	}{
		{"fetch.json", adapter.Date(2026, 3, 2), adapter.Date(2026, 3, 6)},
		{"renumbering.json", adapter.Date(2021, 7, 5), adapter.Date(2021, 7, 13)},
		{"redenomination.json", adapter.Date(2016, 6, 28), adapter.Date(2016, 7, 5)},
	} {
		t.Run(tc.file, func(t *testing.T) {
			g := golden.Load(t, "testdata/golden/"+tc.file)
			rates, err := New(g.Client(t)).Fetch(context.Background(), tc.after, tc.upto)
			if err != nil {
				t.Fatal(err)
			}
			g.Check(t, rates)
		})
	}
}

// recorder answers the currency reference with USD's pre- and post-2021 IDs
// and a monthly ID, and every dynamics request with an empty list.
type recorder struct{ urls []string }

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.urls = append(r.urls, req.URL.String())
	body := `[]`
	if req.URL.Path == "/exrates/currencies" {
		body = `[
			{"Cur_ID":145,"Cur_Abbreviation":"USD","Cur_Scale":1,"Cur_Periodicity":0,
			 "Cur_DateStart":"1995-03-29T00:00:00","Cur_DateEnd":"2021-07-08T00:00:00"},
			{"Cur_ID":431,"Cur_Abbreviation":"USD","Cur_Scale":1,"Cur_Periodicity":0,
			 "Cur_DateStart":"2021-07-09T00:00:00","Cur_DateEnd":"2050-01-01T00:00:00"},
			{"Cur_ID":420,"Cur_Abbreviation":"BRL","Cur_Scale":10,"Cur_Periodicity":1,
			 "Cur_DateStart":"2021-07-09T00:00:00","Cur_DateEnd":"2022-07-31T00:00:00"}
		]`
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: req}, nil
}

func record(t *testing.T, after, upto time.Time) []string {
	t.Helper()
	rec := &recorder{}
	if _, err := New(&http.Client{Transport: rec}).Fetch(context.Background(), after, upto); err != nil {
		t.Fatal(err)
	}
	return rec.urls
}

func TestFetchChunksDynamicsByYear(t *testing.T) {
	want := []string{
		"https://api.nbrb.by/exrates/currencies",
		"https://api.nbrb.by/exrates/rates/dynamics/431?endDate=2024-12-30&startDate=2024-01-01",
		"https://api.nbrb.by/exrates/rates/dynamics/431?endDate=2025-02-01&startDate=2024-12-31",
	}
	if got := record(t, adapter.Date(2024, 1, 1), adapter.Date(2025, 2, 1)); !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFetchAsksEachIDForItsOwnValidity(t *testing.T) {
	want := []string{
		"https://api.nbrb.by/exrates/currencies",
		"https://api.nbrb.by/exrates/rates/dynamics/145?endDate=2021-07-08&startDate=2021-07-01",
		"https://api.nbrb.by/exrates/rates/dynamics/431?endDate=2021-07-12&startDate=2021-07-09",
	}
	if got := record(t, adapter.Date(2021, 7, 1), adapter.Date(2021, 7, 12)); !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFetchStartsNoEarlierThanTheRedenomination(t *testing.T) {
	want := []string{
		"https://api.nbrb.by/exrates/currencies",
		"https://api.nbrb.by/exrates/rates/dynamics/145?endDate=2016-07-05&startDate=2016-07-01",
	}
	for _, after := range []time.Time{{}, adapter.Date(2016, 6, 28)} {
		if got := record(t, after, adapter.Date(2016, 7, 5)); !slices.Equal(got, want) {
			t.Errorf("after %v: got %q, want %q", after, got, want)
		}
	}
}

func TestBackfillRangeIsOneChunk(t *testing.T) {
	if got := New(nil).BackfillRange(); got != 365 {
		t.Errorf("BackfillRange() = %d, want 365", got)
	}
}
