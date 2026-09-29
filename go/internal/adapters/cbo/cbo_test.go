package cbo

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func dataset(t *testing.T) []adapter.Rate {
	t.Helper()
	client := vcrtest.Client(t, "cbo", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI, vcrtest.Body), vcrtest.AllowPlaybackRepeats)
	rates, err := New(client).Fetch(context.Background(), adapter.Date(2026, 6, 1), adapter.Date(2026, 6, 4))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func export(rows string) []byte {
	return []byte(`<table>
  <tr style="font-weight:bold;">
    <td>Currency Code</td><td>Exchange Date</td><td>Currency Name (English)</td><td>Country Name</td><td>Buying</td><td>Selling</td>
  </tr>
  ` + rows + `
</table>`)
}

func mustParse(t *testing.T, html []byte) []adapter.Rate {
	t.Helper()
	rates, err := parse(html)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchRates(t *testing.T) {
	if len(dataset(t)) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := dataset(t)
	first := rates[0].Date
	n := 0
	for _, r := range rates {
		if r.Date.Equal(first) {
			n++
		}
	}
	if n <= 40 {
		t.Errorf("got %d rates on %s, want more than 40", n, first.Format(time.DateOnly))
	}
}

func TestFetchQuotesOMRPerUnitOfForeignCurrency(t *testing.T) {
	rates := dataset(t)
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool {
		return r.Base == "USD" && r.Date.Equal(adapter.Date(2026, 6, 1))
	})
	if i < 0 {
		t.Fatal("no USD rate on 2026-06-01")
	}
	usd := rates[i]
	if usd.Quote != "OMR" {
		t.Errorf("quote = %s, want OMR", usd.Quote)
	}
	if usd.Rate != 0.3845 {
		t.Errorf("rate = %v, want 0.3845", usd.Rate)
	}
}

func TestFetchIncludesPreciousMetalsPerOunce(t *testing.T) {
	rates := dataset(t)
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Base == "XAU" })
	if i < 0 {
		t.Fatal("no XAU rate")
	}
	if rates[i].Rate <= 100 {
		t.Errorf("XAU rate = %v, want > 100", rates[i].Rate)
	}
}

func TestFetchRespectsDateBoundaries(t *testing.T) {
	var dates []time.Time
	for _, r := range dataset(t) {
		if !slices.ContainsFunc(dates, r.Date.Equal) {
			dates = append(dates, r.Date)
		}
	}
	slices.SortFunc(dates, time.Time.Compare)
	want := []time.Time{adapter.Date(2026, 6, 1), adapter.Date(2026, 6, 2), adapter.Date(2026, 6, 3), adapter.Date(2026, 6, 4)}
	if !slices.EqualFunc(dates, want, time.Time.Equal) {
		t.Errorf("dates = %v, want %v", dates, want)
	}
}

func TestParseExportTable(t *testing.T) {
	records := mustParse(t, export(`
<tr><td>USD</td><td>01/06/2026 10:00:00 AM</td><td>United States Dollar</td><td>x</td><td>string;#0.384</td><td>string;#0.385</td></tr>
<tr><td>USD</td><td>02/06/2026 09:55:00 AM</td><td>United States Dollar</td><td>x</td><td>string;#0.384</td><td>string;#0.385</td></tr>
`))
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
	want := adapter.Rate{
		Date: adapter.Date(2026, 6, 1), Base: "USD", Quote: "OMR", Rate: 0.3845,
		Bid: adapter.Float(0.384), Ask: adapter.Float(0.385),
	}
	if !reflect.DeepEqual(records[0], want) {
		t.Errorf("got %+v, want %+v", records[0], want)
	}
}

func TestParseKeepsLatestIntradayRevision(t *testing.T) {
	records := mustParse(t, export(`
<tr><td>EUR</td><td>26/05/2019 08:21:48 AM</td><td>Euro*</td><td>x</td><td>string;#0.4276608</td><td>string;#0.4288515</td></tr>
<tr><td>EUR</td><td>26/05/2019 07:03:44 AM</td><td>Euro*</td><td>x</td><td>string;#0.4301184</td><td>string;#0.4313155</td></tr>
`))
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if records[0].Rate != 0.42825615 {
		t.Errorf("rate = %v, want 0.42825615", records[0].Rate)
	}
}

func TestParseSkipsRowsWithoutUsableBuyAndSell(t *testing.T) {
	records := mustParse(t, export(`
<tr><td>USD</td><td>01/06/2026 10:00:00 AM</td><td>United States Dollar</td><td>x</td><td>string;#</td><td>string;#0.385</td></tr>
<tr><td>USD</td><td>02/06/2026 10:00:00 AM</td><td>United States Dollar</td><td>x</td><td>string;#0</td><td>string;#0.385</td></tr>
`))
	if len(records) != 0 {
		t.Errorf("got %d records, want none", len(records))
	}
}

func TestParseEmptyWindow(t *testing.T) {
	if records := mustParse(t, export("")); len(records) != 0 {
		t.Errorf("got %d records, want none", len(records))
	}
}

func TestParseRaisesWhenNotRatesTable(t *testing.T) {
	_, err := parse([]byte("<html><body>DFESearch</body></html>"))
	if err == nil || !strings.Contains(err.Error(), "rates table") {
		t.Errorf("err = %v, want a missing rates table error", err)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 6, 1), adapter.Date(2026, 6, 4))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

func TestParseLaterRowWinsTimestampTie(t *testing.T) {
	records := mustParse(t, export(`
<tr><td>EUR</td><td>6/5/2019 8:21:48 AM</td><td>Euro*</td><td>x</td><td>string;#0.40</td><td>string;#0.42</td></tr>
<tr><td>EUR</td><td>06/05/2019 08:21:48 AM</td><td>Euro*</td><td>x</td><td>string;#0.50</td><td>string;#0.52</td></tr>
`))
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if !records[0].Date.Equal(adapter.Date(2019, 5, 6)) || records[0].Rate != 0.51 {
		t.Errorf("got %+v, want 2019-05-06 at 0.51", records[0])
	}
}

func TestParseErrorsOnBadTimestamp(t *testing.T) {
	_, err := parse(export(`<tr><td>USD</td><td>2026-06-01</td><td>x</td><td>x</td><td>string;#0.384</td><td>string;#0.385</td></tr>`))
	if err == nil {
		t.Error("want an error for an unparseable timestamp")
	}
}
