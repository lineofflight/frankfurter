package bis

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

const header = "FREQ,REF_AREA,CURRENCY,COLLECTION,TIME_PERIOD,OBS_VALUE,UNIT_MULT\n"

func newAdapter(t *testing.T) *Adapter {
	return New(vcrtest.Client(t, "bis", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
}

// The Ruby spec also backfills these rows and reads them back through the provider API; storage and the API belong to
// core, so this checks the adapter's half: the published digits come out of fetch intact.
func TestPreservesPublishedDigits(t *testing.T) {
	for _, tc := range []struct {
		date  time.Time
		quote string
		rate  float64
	}{
		{adapter.Date(2001, 5, 31), "CDF", 104.4199881839},
		{adapter.Date(2021, 9, 30), "VES", 4128271.016399},
	} {
		t.Run(tc.quote, func(t *testing.T) {
			rates, err := newAdapter(t).Fetch(context.Background(), tc.date, tc.date)
			if err != nil {
				t.Fatal(err)
			}
			var got []adapter.Rate
			for _, r := range rates {
				if r.Quote == tc.quote {
					got = append(got, r)
				}
			}
			if len(got) != 1 || got[0].Rate != tc.rate || !got[0].Date.Equal(tc.date) {
				t.Errorf("got %+v, want one %s row at %v", got, tc.quote, tc.rate)
			}
		})
	}
}

func TestFetchesMonthEndObservationsInWindow(t *testing.T) {
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(2025, 1, 31), adapter.Date(2025, 2, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) <= 100 {
		t.Errorf("got %d rates, want more than 100", len(rates))
	}
	seen := map[string]bool{}
	var jpy float64
	for _, r := range rates {
		if !r.Date.Equal(adapter.Date(2025, 1, 31)) {
			t.Errorf("unexpected date %v", r.Date)
		}
		if r.Base != "USD" {
			t.Errorf("base = %q, want USD", r.Base)
		}
		if seen[r.Quote] {
			t.Errorf("duplicate %s", r.Quote)
		}
		seen[r.Quote] = true
		if r.Quote == "JPY" {
			jpy = r.Rate
		}
	}
	if jpy != 154.902338 {
		t.Errorf("JPY = %v, want 154.902338", jpy)
	}
}

func TestBackfillsEarliestEndPeriodObservation(t *testing.T) {
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(1900, 1, 1), adapter.Date(1900, 1, 31))
	if err != nil {
		t.Fatal(err)
	}
	want := []adapter.Rate{{Date: adapter.Date(1900, 1, 31), Base: "USD", Quote: "ZAR", Rate: 0.4107}}
	if !reflect.DeepEqual(rates, want) {
		t.Errorf("got %+v, want %+v", rates, want)
	}
}

// stub stands in for the adapter with a canned Fetch, as the Ruby specs stub BIS.new and fetch.
type stub struct {
	adapter.Base
	fetch func(after, upto time.Time) []adapter.Rate
}

func (s *stub) BackfillRange() int { return 3650 }

func (s *stub) Fetch(_ context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	return s.fetch(after, upto), nil
}

func TestFetchEachRevisitsPrecedingYear(t *testing.T) {
	today := adapter.Date(2025, 2, 1)
	var source []adapter.Rate
	for _, d := range []time.Time{adapter.Date(2024, 1, 1), adapter.Date(2024, 12, 31), adapter.Date(2025, 1, 31)} {
		source = append(source, adapter.Rate{Date: d, Base: "USD", Quote: "JPY", Rate: 150})
	}
	s := &stub{fetch: func(after, upto time.Time) []adapter.Rate {
		if upto.IsZero() {
			upto = today
		}
		var out []adapter.Rate
		for _, r := range source {
			if !r.Date.Before(after) && !r.Date.After(upto) {
				out = append(out, r)
			}
		}
		return out
	}}
	var dates []time.Time
	err := fetchEach(context.Background(), s, adapter.Date(2025, 1, 31), today, func(rows []adapter.Rate) error {
		for _, r := range rows {
			dates = append(dates, r.Date)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Time{adapter.Date(2024, 12, 31), adapter.Date(2025, 1, 31)}
	if !reflect.DeepEqual(dates, want) {
		t.Errorf("got %v, want %v", dates, want)
	}
}

func TestFetchEachStaysWithinCoverage(t *testing.T) {
	today := adapter.Date(1900, 2, 1)
	var starts []time.Time
	s := &stub{fetch: func(after, _ time.Time) []adapter.Rate {
		starts = append(starts, after)
		return nil
	}}
	flunk := func([]adapter.Rate) error {
		t.Error("yield called")
		return nil
	}
	for _, after := range []time.Time{adapter.Date(1900, 1, 31), adapter.Date(1900, 2, 2)} {
		if err := fetchEach(context.Background(), s, after, today, flunk); err != nil {
			t.Fatal(err)
		}
	}
	want := []time.Time{adapter.Date(1900, 1, 1)}
	if !reflect.DeepEqual(starts, want) {
		t.Errorf("starts = %v, want %v", starts, want)
	}
}

func TestYearBeforeClampsToMonthEnd(t *testing.T) {
	if got := yearBefore(adapter.Date(2024, 2, 29)); !got.Equal(adapter.Date(2023, 2, 28)) {
		t.Errorf("got %v, want 2023-02-28", got)
	}
	if got := yearBefore(adapter.Date(2025, 1, 31)); !got.Equal(adapter.Date(2024, 1, 31)) {
		t.Errorf("got %v, want 2024-01-31", got)
	}
}

// The Ruby spec inserts BIS rows into the rate tables and checks they are not blendable, then checks lookback_days and
// publishes_missed. Those live in core; here the seed must mark BIS as a monthly, non-daily provider, which is what
// keeps it out of blends, on a weekly publish schedule.
func TestKeepsEndPeriodObservationsOutOfBlends(t *testing.T) {
	data, err := os.ReadFile("../../../../db/seeds/providers/bis.json")
	if err != nil {
		t.Fatal(err)
	}
	var seed struct {
		Frequency       string `json:"frequency"`
		PublishCadence  string `json:"publish_cadence"`
		PublishSchedule string `json:"publish_schedule"`
	}
	if err := json.Unmarshal(data, &seed); err != nil {
		t.Fatal(err)
	}
	if seed.Frequency != "monthly" || seed.PublishCadence != "monthly" || seed.PublishSchedule != "0 18 * * 4" {
		t.Errorf("seed = %+v, want monthly frequency and cadence on 0 18 * * 4", seed)
	}
}

func TestParsePreservesDirectionPrecisionAndRestatedUnits(t *testing.T) {
	rates, err := parse([]byte(header + "M,TR,TRY,E,1950-01,3E-06,0\nM,TR,TRY,E,2025-01,35.672530181095,0\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []adapter.Rate{
		{Date: adapter.Date(1950, 1, 31), Base: "USD", Quote: "TRY", Rate: 0.000003},
		{Date: adapter.Date(2025, 1, 31), Base: "USD", Quote: "TRY", Rate: 35.672530181095},
	}
	if !reflect.DeepEqual(rates, want) {
		t.Errorf("got %+v, want %+v", rates, want)
	}
}

type quoteRate struct {
	Quote string
	Rate  float64
}

func quoteRates(t *testing.T, csv string) []quoteRate {
	t.Helper()
	rates, err := parse([]byte(header + csv))
	if err != nil {
		t.Fatal(err)
	}
	var out []quoteRate
	for _, r := range rates {
		out = append(out, quoteRate{r.Quote, r.Rate})
	}
	return out
}

func TestParseSelectsCanonicalCurrencyAreas(t *testing.T) {
	got := quoteRates(t, "M,DE,EUR,E,1990-01,1.2,0\n"+
		"M,XM,EUR,E,1990-01,0.7,0\n"+
		"M,KI,AUD,E,1990-01,1.3,0\n"+
		"M,AU,AUD,E,1990-01,1.4,0\n"+
		"M,GW,XOF,E,1990-01,10,0\n"+
		"M,WA,XOF,E,1990-01,300,0\n"+
		"M,CF,XAF,E,1990-01,301,0\n"+
		"M,CM,XAF,E,1990-01,302,0\n"+
		"M,DM,XCD,E,1990-01,2.6,0\n"+
		"M,AG,XCD,E,1990-01,2.7,0\n"+
		"M,US,USD,E,1990-01,1,0\n")
	want := []quoteRate{{"XEU", 0.7}, {"AUD", 1.4}, {"XOF", 300}, {"XAF", 302}, {"XCD", 2.7}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseKeepsECUSeparateFromEUR(t *testing.T) {
	got := quoteRates(t, "M,XM,EUR,E,1998-12,0.856824,0\nM,XM,EUR,E,1999-01,0.878426,0\n")
	if len(got) != 2 || got[0].Quote != "XEU" || got[1].Quote != "EUR" {
		t.Errorf("got %v, want XEU then EUR", got)
	}
}

func TestParseCorrectsStaleLabelsOnlyInSuccessorUnits(t *testing.T) {
	got := quoteRates(t, "M,SL,SLL,E,2017-12,7.53696,0\n"+
		"M,SL,SLL,E,2022-07,13.88,0\n"+
		"M,VE,VEF,E,2018-07,172368,0\n"+
		"M,VE,VEF,E,2019-06,6550.047641,0\n"+
		"M,MR,MRO,E,2024-08,396,0\n"+
		"M,ST,STD,E,2026-06,21479.9,0\n")
	want := []quoteRate{
		{"SLE", 7.53696}, {"SLE", 13.88}, {"VEF", 172368}, {"VES", 6550.047641}, {"MRO", 396}, {"STD", 21479.9},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseScalesUnitsAndRejectsInvalidValues(t *testing.T) {
	rates, err := parse([]byte(header +
		"M,JP,JPY,E,2024-02,1.5,2\n" +
		"M,JP,JPY,A,2024-02,140,0\n" +
		"D,JP,JPY,E,2024-02-29,150,0\n" +
		"M,CA,CAD,E,2024-02,NaN,0\n" +
		"M,GB,GBP,E,2024-02,,0\n" +
		"M,CH,CHF,E,2024-02,0,0\n" +
		"M,AU,AUD,E,2024-02,-1,0\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []adapter.Rate{{Date: adapter.Date(2024, 2, 29), Base: "USD", Quote: "JPY", Rate: 150}}
	if !reflect.DeepEqual(rates, want) {
		t.Errorf("got %+v, want %+v", rates, want)
	}
}

func TestParseRaisesForUnexpectedContent(t *testing.T) {
	for _, body := range []string{
		"<html>Maintenance</html>",
		"",
		header + "M,JP,JPY,E,garbage,150,0\n",
	} {
		if _, err := parse([]byte(body)); err == nil {
			t.Errorf("parse(%q): want an error", body)
		}
	}
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file        string
		after, upto time.Time
	}{
		{"testdata/golden/fetch.json", adapter.Date(2025, 1, 31), adapter.Date(2025, 2, 1)},
		{"testdata/golden/earliest.json", adapter.Date(1900, 1, 1), adapter.Date(1900, 1, 31)},
		{"testdata/golden/cdf.json", adapter.Date(2001, 5, 31), adapter.Date(2001, 5, 31)},
		{"testdata/golden/ves.json", adapter.Date(2021, 9, 30), adapter.Date(2021, 9, 30)},
	} {
		t.Run(tc.file, func(t *testing.T) {
			g := golden.Load(t, tc.file)
			rates, err := New(g.Client(t)).Fetch(context.Background(), tc.after, tc.upto)
			if err != nil {
				t.Fatal(err)
			}
			g.Check(t, rates)
		})
	}
}
