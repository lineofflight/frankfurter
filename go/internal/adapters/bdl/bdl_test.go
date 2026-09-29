package bdl

import (
	"context"
	"os"
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
	a := New(vcrtest.Client(t, "bdl", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host, vcrtest.Path)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

// fixture reads a workbook written by testdata/fixtures.rb, the Go stand-in for the spec's build_xls.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/fixtures/" + name + ".xls")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func parseFixture(t *testing.T, name string, after, upto time.Time) []adapter.Rate {
	t.Helper()
	rates, err := parse(fixture(t, name), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchLBPQuote(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 9, 1), adapter.Date(2026, 9, 4))
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	for _, r := range rates {
		if r.Quote != "LBP" {
			t.Errorf("quote = %q, want LBP", r.Quote)
		}
	}
}

func TestFetchAllSevenBases(t *testing.T) {
	var bases []string
	for _, r := range fetch(t, adapter.Date(2026, 9, 1), adapter.Date(2026, 9, 4)) {
		bases = append(bases, r.Base)
	}
	slices.Sort(bases)
	bases = slices.Compact(bases)
	want := []string{"AUD", "CAD", "CHF", "EUR", "GBP", "JPY", "USD"}
	if !slices.Equal(bases, want) {
		t.Errorf("bases = %v, want %v", bases, want)
	}
}

func TestFetchFiltersByDateRange(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 9, 1), adapter.Date(2026, 9, 4))
	var dates []time.Time
	for _, r := range rates {
		dates = append(dates, r.Date)
	}
	lo := slices.MinFunc(dates, time.Time.Compare)
	hi := slices.MaxFunc(dates, time.Time.Compare)
	if !lo.Equal(adapter.Date(2026, 9, 1)) || !hi.Equal(adapter.Date(2026, 9, 4)) {
		t.Errorf("dates span %v to %v, want 2026-09-01 to 2026-09-04", lo, hi)
	}
}

func TestFetchPublishedMidAtOfficialRate(t *testing.T) {
	rates := fetch(t, adapter.Date(2026, 9, 4), adapter.Date(2026, 9, 4))
	i := slices.IndexFunc(rates, func(r adapter.Rate) bool { return r.Base == "USD" })
	if i < 0 {
		t.Fatal("no USD rate")
	}
	if rates[i].Rate != 89_500.0 {
		t.Errorf("USD = %v, want 89500", rates[i].Rate)
	}
}

type dated struct {
	date time.Time
	rate float64
}

func TestParseReadsEverySheet(t *testing.T) {
	var got []dated
	for _, r := range parseFixture(t, "sheets", time.Time{}, time.Time{}) {
		got = append(got, dated{r.Date, r.Rate})
	}
	want := []dated{
		{adapter.Date(2025, 1, 2), 89_500.0},
		{adapter.Date(2024, 12, 30), 89_500.0},
		{adapter.Date(2024, 1, 2), 15_000.0},
	}
	if !slices.EqualFunc(got, want, func(a, b dated) bool { return a.date.Equal(b.date) && a.rate == b.rate }) {
		t.Errorf("got %v, want %v", got, want)
	}

	filtered := parseFixture(t, "sheets", adapter.Date(2024, 12, 1), adapter.Date(2025, 1, 1))
	if len(filtered) != 1 || !filtered[0].Date.Equal(adapter.Date(2024, 12, 30)) {
		t.Errorf("filtered = %+v, want one row on 2024-12-30", filtered)
	}
}

func TestParsePublishedMid(t *testing.T) {
	got := parseFixture(t, "mid", time.Time{}, time.Time{})
	want := adapter.Rate{Date: adapter.Date(2024, 1, 2), Base: "USD", Quote: "LBP", Rate: 15_000.0}
	if len(got) != 1 || !got[0].Date.Equal(want.Date) || got[0].Base != want.Base || got[0].Quote != want.Quote ||
		got[0].Rate != want.Rate || got[0].Bid != nil || got[0].Ask != nil || got[0].Mid != nil {
		t.Errorf("got %+v, want [%+v]", got, want)
	}
}

func TestParseDropsMissingOrNonPositiveMid(t *testing.T) {
	var bases []string
	for _, r := range parseFixture(t, "missing_mid", time.Time{}, time.Time{}) {
		bases = append(bases, r.Base)
	}
	if !slices.Equal(bases, []string{"GBP"}) {
		t.Errorf("bases = %v, want [GBP]", bases)
	}
}

func TestParseCollapsesVerbatimDuplicates(t *testing.T) {
	if got := parseFixture(t, "duplicates", time.Time{}, time.Time{}); len(got) != 1 {
		t.Errorf("got %d rows, want 1", len(got))
	}
}

func TestParseRaisesOnConflictingMids(t *testing.T) {
	_, err := parse(fixture(t, "conflict"), time.Time{}, time.Time{})
	if err == nil {
		t.Fatal("no error")
	}
	if !strings.Contains(err.Error(), "USD 2025-11-17") {
		t.Errorf("error %q lacks USD 2025-11-17", err)
	}
}

func TestParseRaisesOnNonOLE2Payload(t *testing.T) {
	if _, err := parse([]byte("not a workbook"), time.Time{}, time.Time{}); err == nil {
		t.Fatal("no error")
	}
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		file        string
		after, upto time.Time
	}{
		{"fetch.json", adapter.Date(2026, 9, 1), adapter.Date(2026, 9, 4)},
		{"archive.json", time.Time{}, time.Time{}},
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
