package ust

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "ust", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 6, 30), adapter.Date(2026, 8, 31))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchQuarterFromStartDateInclusive(t *testing.T) {
	rates := fetch(t)
	if len(rates) <= 100 {
		t.Fatalf("got %d rates, want more than 100", len(rates))
	}
	first := rates[0].Date
	for _, r := range rates {
		if r.Date.Before(first) {
			first = r.Date
		}
		if r.Base != "USD" {
			t.Errorf("base = %q, want USD", r.Base)
		}
	}
	if !first.Equal(adapter.Date(2026, 6, 30)) {
		t.Errorf("first date = %v, want 2026-06-30", first)
	}
}

func TestFetchKeepsMidQuarterAmendments(t *testing.T) {
	var krw []adapter.Rate
	for _, r := range fetch(t) {
		if r.Quote == "KRW" {
			krw = append(krw, r)
		}
	}
	sort.Slice(krw, func(i, j int) bool { return krw[i].Date.Before(krw[j].Date) })
	var dates []time.Time
	for _, r := range krw {
		dates = append(dates, r.Date)
	}
	want := []time.Time{adapter.Date(2026, 6, 30), adapter.Date(2026, 8, 31)}
	if !reflect.DeepEqual(dates, want) {
		t.Fatalf("KRW dates = %v, want %v", dates, want)
	}
	if krw[1].Rate != 1367.46 {
		t.Errorf("amended KRW rate = %v, want 1367.46", krw[1].Rate)
	}
}

func TestFetchEmitsOneRowPerPairAndDate(t *testing.T) {
	type key struct {
		date  time.Time
		quote string
	}
	seen := map[key]bool{}
	for _, r := range fetch(t) {
		k := key{r.Date, r.Quote}
		if seen[k] {
			t.Errorf("duplicate row for %v %s", r.Date, r.Quote)
		}
		seen[k] = true
	}
}

func rec(label, rate, record, effective string) row {
	if effective == "" {
		effective = record
	}
	return row{RecordDate: record, EffectiveDate: effective, Label: label, ExchangeRate: rate}
}

func mustParse(t *testing.T, rows ...row) []adapter.Rate {
	t.Helper()
	rates, err := parse(rows)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestParseMapsLabelsToISOCodes(t *testing.T) {
	got := mustParse(t, rec("Norway-Krone", "9.916", "2026-06-30", ""))
	want := []adapter.Rate{{Date: adapter.Date(2026, 6, 30), Base: "USD", Quote: "NOK", Rate: 9.916}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestParseDropsUnmappedLabels(t *testing.T) {
	got := mustParse(t, rec("Cross Border-Euro", "0.877", "2026-06-30", ""), rec("Ecuador-Dolares", "1.0", "2026-06-30", ""))
	if len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
}

func TestParseFirstListedLabelWinsSharedPair(t *testing.T) {
	got := mustParse(t, rec("Togo-Cfa Franc", "570.0", "2026-06-30", ""), rec("Benin-Cfa Franc", "571.5", "2026-06-30", ""))
	if len(got) != 1 {
		t.Fatalf("got %d rates, want 1", len(got))
	}
	if got[0].Rate != 571.5 {
		t.Errorf("rate = %v, want 571.5", got[0].Rate)
	}
}

func TestParseKeepsPredecessorBeforeUnitChange(t *testing.T) {
	old := mustParse(t, rec("Turkey-Lira", "1418000.0", "2004-06-30", ""))
	late := mustParse(t, rec("Turkey-Lira", "5.755", "2019-06-30", ""))
	if len(old) != 1 || old[0].Quote != "TRL" {
		t.Errorf("old = %+v, want TRL", old)
	}
	if len(late) != 0 {
		t.Errorf("late = %+v, want none", late)
	}
}

func TestParseSwitchesCodeAtSourceSwitchDate(t *testing.T) {
	before := mustParse(t, rec("Mauritania-Ouguiya", "355.0", "2018-03-31", ""))
	after := mustParse(t, rec("Mauritania-Ouguiya", "35.5", "2018-06-30", ""))
	if len(before) != 1 || before[0].Quote != "MRO" {
		t.Errorf("before = %+v, want MRO", before)
	}
	if len(after) != 1 || after[0].Quote != "MRU" {
		t.Errorf("after = %+v, want MRU", after)
	}
}

// Treasury switched unit with a mid-quarter amendment: old leone through 2022-06-30, new leone from the 2022-07-15
// amendment. Both sides are real rows.
func TestParseKeepsEveryLeoneRowThroughRedenomination(t *testing.T) {
	june := mustParse(t, rec("Sierra Leone-Leone", "13175.0", "2022-06-30", ""))
	amended := mustParse(t, rec("Sierra Leone-Leone", "13.62", "2022-06-30", "2022-07-15"))
	if len(june) != 1 || june[0].Quote != "SLL" {
		t.Errorf("june = %+v, want SLL", june)
	}
	if len(amended) != 1 || amended[0].Quote != "SLE" {
		t.Errorf("amended = %+v, want SLE", amended)
	}
}

func TestParseDatesAmendmentsOnEffectiveDate(t *testing.T) {
	got := mustParse(t, rec("Korea-Won", "1367.46", "2026-06-30", "2026-08-31"))
	if len(got) != 1 || !got[0].Date.Equal(adapter.Date(2026, 8, 31)) {
		t.Errorf("got %+v, want dated 2026-08-31", got)
	}
}

func TestMonthsBeforeClampsLikeRuby(t *testing.T) {
	for _, tc := range []struct{ in, want time.Time }{
		{adapter.Date(2026, 6, 30), adapter.Date(2026, 2, 28)},
		{adapter.Date(2024, 6, 30), adapter.Date(2024, 2, 29)},
		{adapter.Date(2026, 3, 15), adapter.Date(2025, 11, 15)},
		{adapter.Date(2026, 1, 31), adapter.Date(2025, 9, 30)},
	} {
		if got := monthsBefore(tc.in, 4); !got.Equal(tc.want) {
			t.Errorf("monthsBefore(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 6, 30), adapter.Date(2026, 8, 31))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
