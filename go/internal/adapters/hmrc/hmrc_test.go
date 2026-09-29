package hmrc

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func newAdapter(t *testing.T) *Adapter {
	return New(vcrtest.Client(t, "hmrc", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
}

func today(y int, m time.Month, d int) func() time.Time {
	return func() time.Time { return time.Date(y, m, d, 12, 0, 0, 0, time.UTC) }
}

func uniqueDates(rates []adapter.Rate) []time.Time {
	var dates []time.Time
	for _, r := range rates {
		if !slices.ContainsFunc(dates, r.Date.Equal) {
			dates = append(dates, r.Date)
		}
	}
	return dates
}

func equalDates(got, want []time.Time) bool {
	return slices.EqualFunc(got, want, time.Time.Equal)
}

const header = "Country/Territories,Currency,Currency Code,Currency Units per £1,Start date,End date\n"

func mustParse(t *testing.T, csv string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func quotes(rates []adapter.Rate) []string {
	var qs []string
	for _, r := range rates {
		qs = append(qs, r.Quote)
	}
	return qs
}

func TestRevises(t *testing.T) {
	if !New(nil).Revises() {
		t.Error("Revises() = false, want true")
	}
}

func TestFetchMonthlyRatesOnEffectiveDates(t *testing.T) {
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(2026, 8, 1), adapter.Date(2026, 9, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) <= 100 {
		t.Errorf("got %d rates, want > 100", len(rates))
	}
	dates := uniqueDates(rates)
	slices.SortFunc(dates, time.Time.Compare)
	if want := []time.Time{adapter.Date(2026, 8, 1), adapter.Date(2026, 9, 1)}; !equalDates(dates, want) {
		t.Errorf("dates = %v, want %v", dates, want)
	}
	for _, r := range rates {
		if r.Base != "GBP" {
			t.Fatalf("base = %q, want GBP", r.Base)
		}
	}
}

func TestFetchOneRowPerPairAndDate(t *testing.T) {
	rates, err := newAdapter(t).Fetch(context.Background(), adapter.Date(2026, 9, 1), adapter.Date(2026, 9, 1))
	if err != nil {
		t.Fatal(err)
	}
	qs := quotes(rates)
	slices.Sort(qs)
	if len(slices.Compact(slices.Clone(qs))) != len(qs) {
		t.Error("duplicate quotes")
	}
}

func TestFetchFollowingMonthAsSoonAsItExists(t *testing.T) {
	a := newAdapter(t)
	a.Now = today(2026, 8, 20)
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 8, 1), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := uniqueDates(rates), []time.Time{adapter.Date(2026, 8, 1), adapter.Date(2026, 9, 1)}; !equalDates(got, want) {
		t.Errorf("dates = %v, want %v", got, want)
	}
}

func TestFetchToleratesUnpublishedFollowingMonth(t *testing.T) {
	a := newAdapter(t)
	a.Now = today(2026, 9, 11)
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 9, 1), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := uniqueDates(rates), []time.Time{adapter.Date(2026, 9, 1)}; !equalDates(got, want) {
		t.Errorf("dates = %v, want %v", got, want)
	}
}

func TestFetchRaisesWhenPastMonthMissing(t *testing.T) {
	a := newAdapter(t)
	a.Now = today(2021, 1, 15)
	_, err := a.Fetch(context.Background(), adapter.Date(2020, 12, 1), adapter.Date(2020, 12, 31))
	var se *adapter.StatusError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want *adapter.StatusError", err)
	}
}

func TestParseMapsRatesWithGBPBase(t *testing.T) {
	rates := mustParse(t, header+"Eurozone,Euro,EUR,1.1681,01/09/2026,30/09/2026\n")
	want := []adapter.Rate{{Date: adapter.Date(2026, 9, 1), Base: "GBP", Quote: "EUR", Rate: 1.1681}}
	if !slices.Equal(rates, want) {
		t.Errorf("got %+v, want %+v", rates, want)
	}
}

func TestParseDeduplicatesSharedCurrencyCode(t *testing.T) {
	rates := mustParse(t, header+
		"Benin,CFA Franc,XOF,766.2127,01/09/2026,30/09/2026\n"+
		"Senegal,CFA Franc,XOF,766.2127,01/09/2026,30/09/2026\n")
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	if rates[0].Quote != "XOF" || rates[0].Rate != 766.2127 {
		t.Errorf("got %+v", rates[0])
	}
}

func TestParseMapsNonISOLabels(t *testing.T) {
	rates := mustParse(t, header+
		"Venezuela,Venezuelan Bolivar,VED,1047.3299,01/09/2026,30/09/2026\n"+
		"Zimbabwe,Zimbabwe Gold,ZIG,36.2052,01/09/2026,30/09/2026\n")
	if got, want := quotes(rates), []string{"VES", "ZWG"}; !slices.Equal(got, want) {
		t.Errorf("quotes = %v, want %v", got, want)
	}
}

func TestParseLeavesRetiredSucreAlone(t *testing.T) {
	rates := mustParse(t, header+
		"Ecuador,Dollar,ECS,1.3554,01/09/2026,30/09/2026\n"+
		"USA,Dollar,USD,1.3554,01/09/2026,30/09/2026\n")
	if got, want := quotes(rates), []string{"ECS", "USD"}; !slices.Equal(got, want) {
		t.Errorf("quotes = %v, want %v", got, want)
	}
}

func TestParseKeepsOldLeoneUnderRealCode(t *testing.T) {
	rates := mustParse(t, header+
		"Sierra Leone,Leone,SLE,15631.1792,01/11/2022,30/11/2022\n"+
		"Sierra Leone,Leone,SLE,23.9705,01/03/2023,31/03/2023\n")
	if len(rates) != 2 ||
		!rates[0].Date.Equal(adapter.Date(2022, 11, 1)) || rates[0].Quote != "SLL" ||
		!rates[1].Date.Equal(adapter.Date(2023, 3, 1)) || rates[1].Quote != "SLE" {
		t.Errorf("got %+v", rates)
	}
}

func TestParseKeepsMidMonthCorrection(t *testing.T) {
	rates := mustParse(t, header+
		"USA,Dollar,USD,1.3554,01/10/2026,14/10/2026\n"+
		"USA,Dollar,USD,1.2900,15/10/2026,31/10/2026\n")
	if len(rates) != 2 ||
		!rates[0].Date.Equal(adapter.Date(2026, 10, 1)) || rates[0].Rate != 1.3554 ||
		!rates[1].Date.Equal(adapter.Date(2026, 10, 15)) || rates[1].Rate != 1.29 {
		t.Errorf("got %+v", rates)
	}
}

func TestParseRaisesOnMissingColumns(t *testing.T) {
	_, err := parse([]byte("Country,Currency,Code,Rate,From,To\nUSA,Dollar,USD,1.3554,01/09/2026,30/09/2026\n"))
	if err == nil || !strings.Contains(err.Error(), "HMRC") {
		t.Errorf("err = %v, want one mentioning HMRC", err)
	}
}

// Ruby tags a charset-less body BINARY; Go has no encoding tags, so raw bytes
// are the only case.
func TestParseBodyWithoutUTF8Tag(t *testing.T) {
	if rates := mustParse(t, header+"USA,Dollar,USD,1.3554,01/09/2026,30/09/2026\n"); len(rates) != 1 {
		t.Errorf("got %d rates, want 1", len(rates))
	}
}

func TestParseDropsNonPositiveOrUnparseableRates(t *testing.T) {
	rates := mustParse(t, header+
		"Nowhere,Zero,XYZ,0.0,01/09/2026,30/09/2026\n"+
		"Nowhere,Negative,ABC,-1.5,01/09/2026,30/09/2026\n"+
		"Nowhere,Invalid,DEF,N/A,01/09/2026,30/09/2026\n")
	if len(rates) != 0 {
		t.Errorf("got %+v, want none", rates)
	}
}

// Ruby's CSV reads empty fields as nil, and parse skips rows missing a code,
// date or rate.
func TestParseSkipsRowsWithEmptyFields(t *testing.T) {
	rates := mustParse(t, header+
		"USA,Dollar,,1.3554,01/09/2026,30/09/2026\n"+
		"USA,Dollar,USD,1.3554,,30/09/2026\n"+
		"USA,Dollar,USD,,01/09/2026,30/09/2026\n"+
		"USA,Dollar\n"+
		"Eurozone,Euro,EUR,1.1681,01/09/2026,30/09/2026\n")
	if got, want := quotes(rates), []string{"EUR"}; !slices.Equal(got, want) {
		t.Errorf("quotes = %v, want %v", got, want)
	}
}

func TestParseRaisesOnMalformedCSV(t *testing.T) {
	if _, err := parse([]byte(header + "USA,Dol\"lar,USD,1.5,01/09/2026,30/09/2026\n")); err == nil {
		t.Error("err = nil, want a CSV error")
	}
}

func TestParseRaisesOnBadStartDate(t *testing.T) {
	if _, err := parse([]byte(header + "USA,Dollar,USD,1.5,2026-09-01,30/09/2026\n")); err == nil {
		t.Error("err = nil, want a date error")
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	rates, err := New(g.Client(t)).Fetch(context.Background(), adapter.Date(2026, 8, 1), adapter.Date(2026, 9, 1))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}

func TestGoldenUnpublished(t *testing.T) {
	g := golden.Load(t, "testdata/golden/unpublished.json")
	a := New(g.Client(t))
	a.Now = g.Now(t)
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 9, 1), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
