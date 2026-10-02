package jpc

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/pdftext"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func newAdapter(t *testing.T) *Adapter {
	return New(vcrtest.Client(t, "jpc", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
}

func fetch(t *testing.T, after, upto time.Time) []adapter.Rate {
	t.Helper()
	rates, err := newAdapter(t).Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

// dates returns the distinct dates in order of appearance.
func dates(rates []adapter.Rate) []time.Time {
	var ds []time.Time
	for _, r := range rates {
		if !slices.ContainsFunc(ds, r.Date.Equal) {
			ds = append(ds, r.Date)
		}
	}
	return ds
}

func find(rates []adapter.Rate, date time.Time, base string) (adapter.Rate, bool) {
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool {
		return r.Base == base && (date.IsZero() || r.Date.Equal(date))
	})
	if i < 0 {
		return adapter.Rate{}, false
	}
	return rates[i], true
}

func wantRate(t *testing.T, rates []adapter.Rate, date time.Time, base string, want float64) {
	t.Helper()
	r, ok := find(rates, date, base)
	if !ok {
		t.Errorf("no %s rate", base)
		return
	}
	if r.Rate != want {
		t.Errorf("%s = %v, want %v", base, r.Rate, want)
	}
}

func wantDates(t *testing.T, rates []adapter.Rate, want ...time.Time) {
	t.Helper()
	if got := dates(rates); !slices.EqualFunc(got, want, time.Time.Equal) {
		t.Errorf("dates = %v, want %v", got, want)
	}
}

func TestFetchSundayObservations(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 9, 20), adapter.Date(2026, 9, 27))
	if len(rates) != 168 {
		t.Errorf("got %d rates, want 168", len(rates))
	}
	wantDates(t, rates, adapter.Date(2026, 9, 20), adapter.Date(2026, 9, 27))
	for _, r := range rates {
		if r.Quote != "JPY" {
			t.Fatalf("quote %s, want JPY", r.Quote)
		}
	}
	wantRate(t, rates, adapter.Date(2026, 9, 27), "USD", 155.39)
	wantRate(t, rates, adapter.Date(2026, 9, 27), "KRW", 0.1141)
	if _, ok := find(rates, time.Time{}, "BND"); ok {
		t.Error("BND published only as an equivalence, want no rate")
	}
}

func TestFetchComingWeek(t *testing.T) {
	a := newAdapter(t)
	a.Now = func() time.Time { return time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC) }
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 9, 21), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	wantDates(t, rates, adapter.Date(2026, 9, 27))

	// What ingest validation keeps: positive rates dated within two days plus
	// the lead of today.
	horizon := a.Today().AddDate(0, 0, 2+a.LeadDays())
	kept := 0
	for _, r := range rates {
		if r.Rate > 0 && !r.Date.After(horizon) {
			kept++
		}
	}
	if kept != 84 {
		t.Errorf("validation keeps %d rates, want 84", kept)
	}
}

func TestFetchClipsToEffectiveDates(t *testing.T) {
	if rates := fetch(t, adapter.Date(2026, 9, 21), adapter.Date(2026, 9, 26)); len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestFetchEarliestArchive(t *testing.T) {
	rates := fetch(t, adapter.Date(2002, 1, 1), adapter.Date(2002, 1, 6))
	if len(rates) != 107 {
		t.Errorf("got %d rates, want 107", len(rates))
	}
	wantDates(t, rates, adapter.Date(2002, 1, 6))
	wantRate(t, rates, time.Time{}, "USD", 130.97)
	wantRate(t, rates, time.Time{}, "KRW", 0.1003)
	wantRate(t, rates, time.Time{}, "IDR", 0.0129)
	if _, ok := find(rates, time.Time{}, "TRL"); !ok {
		t.Error("no TRL rate")
	}
	wantRate(t, rates, time.Time{}, "RUB", 4.34)
	wantRate(t, rates, time.Time{}, "YUM", 1.97)
}

func TestFetchRedenominatedLabels(t *testing.T) {
	rates := fetch(t, adapter.Date(2002, 1, 6), adapter.Date(2002, 1, 6))
	wantRate(t, rates, time.Time{}, "PLN", 33.01)
	wantRate(t, rates, time.Time{}, "BGN", 60.17)
	for _, stale := range []string{"PLZ", "BGL"} {
		if _, ok := find(rates, time.Time{}, stale); ok {
			t.Errorf("stale label %s kept", stale)
		}
	}
}

func TestFetchAcrossArchiveIndexBoundary(t *testing.T) {
	rates := fetch(t, adapter.Date(2007, 12, 30), adapter.Date(2008, 1, 6))
	wantDates(t, rates, adapter.Date(2007, 12, 30), adapter.Date(2008, 1, 6))
	wantRate(t, rates, adapter.Date(2008, 1, 6), "USD", 114.13)
}

func TestFetchWeekSpanningYears(t *testing.T) {
	rates := fetch(t, adapter.Date(2019, 12, 29), adapter.Date(2020, 1, 5))
	wantDates(t, rates, adapter.Date(2019, 12, 29), adapter.Date(2020, 1, 5))
}

func TestWeeklyObservationsStayOutOfBlends(t *testing.T) {
	data, err := os.ReadFile("../../../../db/seeds/providers/jpc.json")
	if err != nil {
		t.Fatal(err)
	}
	var seed struct {
		Frequency string `json:"frequency"`
	}
	if err := json.Unmarshal(data, &seed); err != nil {
		t.Fatal(err)
	}
	if seed.Frequency == "" || seed.Frequency == "daily" {
		t.Errorf("frequency = %q, want a non-daily frequency so JPC never blends", seed.Frequency)
	}
}

func run(text string, x, y float64) pdftext.Run {
	return pdftext.Run{X: x, Y: y, Width: 20, FontSize: 10, Text: text}
}

var sunday = adapter.Date(2026, 9, 27)

func TestParseRunsUnitColumns(t *testing.T) {
	pages := [][]pdftext.Run{{
		run("USD", 360, 100), run("155.39", 440, 100),
		run("KRW", 360, 80), run("11.41", 520, 79),
		run("IDR", 360, 60), run("0.88", 520, 59),
	}}
	got, err := parseRuns(pages, sunday)
	if err != nil {
		t.Fatal(err)
	}
	want := []adapter.Rate{
		{Date: sunday, Base: "USD", Quote: "JPY", Rate: 155.39},
		{Date: sunday, Base: "KRW", Quote: "JPY", Rate: 0.1141},
		{Date: sunday, Base: "IDR", Quote: "JPY", Rate: 0.0088},
	}
	if !slices.Equal(got, want) {
		t.Errorf("parseRuns = %+v, want %+v", got, want)
	}
}

func TestParseRunsYugoslavDinar(t *testing.T) {
	pages := [][]pdftext.Run{{
		run("YUN", 360, 100), run("1.80", 440, 100),
		run("KRW", 360, 80), run("9.86", 520, 79),
	}}
	before, err := parseRuns(pages, adapter.Date(2003, 1, 26))
	if err != nil {
		t.Fatal(err)
	}
	after, err := parseRuns(pages, adapter.Date(2003, 2, 2))
	if err != nil {
		t.Fatal(err)
	}
	if before[0].Base != "YUM" {
		t.Errorf("before cutover = %s, want YUM", before[0].Base)
	}
	if after[0].Base != "CSD" || after[0].Rate != 1.80 {
		t.Errorf("after cutover = %+v, want CSD at 1.80", after[0])
	}
}

func TestParseRunsSkipsProseAndZeroRates(t *testing.T) {
	pages := [][]pdftext.Run{{
		run("USD", 360, 100), run("155.39", 440, 100),
		run("KRW", 360, 80), run("11.41", 520, 79),
		run("BND", 360, 60), run("Equivalent to Singapore dollar", 420, 59),
		run("XYZ", 360, 40), run("0.00", 440, 39),
	}}
	got, err := parseRuns(pages, sunday)
	if err != nil {
		t.Fatal(err)
	}
	var bases []string
	for _, r := range got {
		bases = append(bases, r.Base)
	}
	if want := []string{"USD", "KRW"}; !slices.Equal(bases, want) {
		t.Errorf("bases = %v, want %v", bases, want)
	}
}

func TestParseRunsErrors(t *testing.T) {
	tests := map[string][][]pdftext.Run{
		"two values for one currency": {{run("USD", 360, 100), run("155.39", 440, 100), run("15539.00", 520, 100)}},
		"a third numeric column": {{
			run("USD", 360, 100), run("155.39", 440, 100),
			run("KRW", 360, 80), run("11.41", 520, 79),
			run("EUR", 360, 60), run("179.12", 480, 59),
		}},
		"no pages":        {},
		"a single column": {{run("USD", 360, 100), run("155.39", 440, 100)}},
	}
	for name, pages := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseRuns(pages, sunday); err == nil {
				t.Error("want an error")
			}
		})
	}
}

func TestGolden(t *testing.T) {
	tests := []struct {
		file        string
		after, upto time.Time
	}{
		{"testdata/golden/fetch.json", adapter.Date(2026, 9, 20), adapter.Date(2026, 9, 27)},
		{"testdata/golden/coming_week.json", adapter.Date(2026, 9, 21), time.Time{}},
		{"testdata/golden/archive.json", adapter.Date(2002, 1, 1), adapter.Date(2002, 1, 6)},
		{"testdata/golden/index_boundary.json", adapter.Date(2007, 12, 30), adapter.Date(2008, 1, 6)},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			g := golden.Load(t, tt.file)
			a := New(g.Client(t))
			a.Now = g.Now(t)
			rates, err := a.Fetch(context.Background(), tt.after, tt.upto)
			if err != nil {
				t.Fatal(err)
			}
			g.Check(t, rates)
		})
	}
}
