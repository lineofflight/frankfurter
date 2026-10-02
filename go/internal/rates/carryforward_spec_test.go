package rates_test

// Ports spec/carry_forward_spec.rb.

import (
	"fmt"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/rates"
)

func cfDate(m time.Month, d int) time.Time { return time.Date(2024, m, d, 0, 0, 0, 0, time.UTC) }

func cfRow(date time.Time, provider, base, quote string, rate float64) rates.Row {
	return rates.Row{Date: date, Provider: provider, Base: base, Quote: quote, Rate: rate}
}

func TestApplyReturnsMostRecentPerSeries(t *testing.T) {
	rows := []rates.Row{
		cfRow(cfDate(1, 5), "ECB", "EUR", "USD", 1.08),
		cfRow(cfDate(1, 4), "ECB", "EUR", "USD", 1.07),
		cfRow(cfDate(1, 5), "BOC", "CAD", "USD", 0.74),
	}
	got := rates.CarryForward(rows, cfDate(1, 6), rates.LookbackDays)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	for _, r := range got {
		if r.Provider == "ECB" && (!r.Date.Equal(cfDate(1, 5)) || r.Rate != 1.08) {
			t.Errorf("ECB = %+v", r)
		}
	}
}

func TestApplyExcludesOutsideLookback(t *testing.T) {
	rows := []rates.Row{cfRow(cfDate(1, 1), "ECB", "EUR", "USD", 1.08)}
	if got := rates.CarryForward(rows, cfDate(1, 20), 14); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestApplyIncludesLookbackBoundary(t *testing.T) {
	rows := []rates.Row{cfRow(cfDate(1, 1), "ECB", "EUR", "USD", 1.08)}
	if got := rates.CarryForward(rows, cfDate(1, 15), 14); len(got) != 1 {
		t.Errorf("got %+v", got)
	}
}

func TestApplyExcludesAfterTarget(t *testing.T) {
	rows := []rates.Row{cfRow(cfDate(1, 10), "ECB", "EUR", "USD", 1.08)}
	if got := rates.CarryForward(rows, cfDate(1, 9), rates.LookbackDays); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestApplyHandlesMultipleCurrencies(t *testing.T) {
	rows := []rates.Row{
		cfRow(cfDate(1, 5), "ECB", "EUR", "USD", 1.08),
		cfRow(cfDate(1, 3), "ECB", "EUR", "GBP", 0.86),
	}
	got := rates.CarryForward(rows, cfDate(1, 6), rates.LookbackDays)
	var quotes []string
	for _, r := range got {
		quotes = append(quotes, r.Quote)
	}
	sort.Strings(quotes)
	if !slices.Equal(quotes, []string{"GBP", "USD"}) {
		t.Errorf("quotes = %v", quotes)
	}
}

func TestApplyEmptyInput(t *testing.T) {
	if got := rates.CarryForward(nil, cfDate(1, 6), rates.LookbackDays); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

// assertMatchesApply checks that EachSnapshot yields, for every date, the set
// CarryForward (the trusted oracle) returns. Sets are compared because
// downstream blending is order-independent.
func assertMatchesApply(t *testing.T, rows []rates.Row, dates []time.Time, lookback int) {
	t.Helper()
	normalize := func(rows []rates.Row) []string {
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = fmt.Sprint(r.Provider, r.Base, r.Quote, r.Date.Format(time.DateOnly), r.Rate)
		}
		sort.Strings(out)
		return out
	}
	collected := map[time.Time][]rates.Row{}
	var yielded []time.Time
	rates.EachSnapshot(rows, dates, lookback, func(d time.Time, contributors []rates.Row) {
		collected[d] = contributors
		yielded = append(yielded, d)
	})
	want := slices.Clone(dates)
	slices.SortFunc(want, time.Time.Compare)
	if !slices.EqualFunc(yielded, want, time.Time.Equal) {
		t.Fatalf("yielded %v, want %v", yielded, want)
	}
	for _, d := range dates {
		got, exp := normalize(collected[d]), normalize(rates.CarryForward(rows, d, lookback))
		if !slices.Equal(got, exp) {
			t.Errorf("%s: got %v, want %v", d.Format(time.DateOnly), got, exp)
		}
	}
}

func dayRange(from, to time.Time) []time.Time {
	var out []time.Time
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		out = append(out, d)
	}
	return out
}

func TestEachSnapshotMatchesApplyAcrossSeries(t *testing.T) {
	var rows []rates.Row
	for i := range 40 {
		date := cfDate(1, 1).AddDate(0, 0, i+1)
		if wd := date.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue // weekend gaps, like production
		}
		step := float64(i) * 0.001
		rows = append(rows,
			cfRow(date, "ECB", "EUR", "USD", 1.08+step),
			cfRow(date, "ECB", "EUR", "GBP", 0.86+step),
			cfRow(date, "BOC", "CAD", "USD", 0.74+step))
	}
	assertMatchesApply(t, rows, dayRange(cfDate(1, 1), cfDate(2, 20)), rates.LookbackDays)
}

func TestEachSnapshotMatchesApplyAsRowsAgeOut(t *testing.T) {
	rows := []rates.Row{
		cfRow(cfDate(1, 1), "ECB", "EUR", "USD", 1.08),
		cfRow(cfDate(1, 2), "BOC", "CAD", "USD", 0.74),
		cfRow(cfDate(1, 30), "ECB", "EUR", "USD", 1.09),
	}
	assertMatchesApply(t, rows, dayRange(cfDate(1, 1), cfDate(2, 5)), rates.LookbackDays)
}

func TestEachSnapshotMatchesApplyBeforeData(t *testing.T) {
	rows := []rates.Row{cfRow(cfDate(6, 1), "ECB", "EUR", "USD", 1.08)}
	assertMatchesApply(t, rows, []time.Time{cfDate(1, 1), cfDate(5, 1), cfDate(6, 1)}, rates.LookbackDays)
}

func TestEachSnapshotMatchesApplyCustomLookback(t *testing.T) {
	rows := []rates.Row{
		cfRow(cfDate(1, 1), "ECB", "EUR", "USD", 1.08),
		cfRow(cfDate(1, 5), "ECB", "EUR", "USD", 1.09),
	}
	assertMatchesApply(t, rows, dayRange(cfDate(1, 1), cfDate(1, 20)), 3)
}

func TestEachSnapshotUnsortedAnchors(t *testing.T) {
	rows := []rates.Row{cfRow(cfDate(1, 10), "ECB", "EUR", "USD", 1.08)}
	assertMatchesApply(t, rows, []time.Time{cfDate(1, 15), cfDate(1, 10), cfDate(1, 12)}, rates.LookbackDays)
}

func TestEachSnapshotYieldsEmptyContributors(t *testing.T) {
	yielded := false
	rates.EachSnapshot(nil, []time.Time{cfDate(1, 1)}, rates.LookbackDays, func(time.Time, []rates.Row) { yielded = true })
	if !yielded {
		t.Error("no yield")
	}
}
