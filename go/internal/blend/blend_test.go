package blend

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

var (
	jan15 = adapter.Date(2024, 1, 15)
	far   = adapter.Date(2100, 1, 1) // a today that never caps the reference date
)

func row(date time.Time, base, quote string, rate float64, provider string) rates.Row {
	return rates.Row{Date: date, Base: base, Quote: quote, Rate: rate, Provider: provider}
}

func plain(rows []rates.Row) []Annotated {
	out := make([]Annotated, len(rows))
	for i, r := range rows {
		out[i] = Annotated{Row: r}
	}
	return out
}

func findBlended(t *testing.T, rows []currency.Blended, quote string) currency.Blended {
	t.Helper()
	for _, r := range rows {
		if r.Quote == quote {
			return r
		}
	}
	t.Fatalf("no %s row in %+v", quote, rows)
	return currency.Blended{}
}

func findRow(t *testing.T, rows []rates.Row, quote string) rates.Row {
	t.Helper()
	for _, r := range rows {
		if r.Quote == quote {
			return r
		}
	}
	t.Fatalf("no %s row in %+v", quote, rows)
	return rates.Row{}
}

// closeTo is minitest's must_be_close_to, whose default delta is 0.001.
func closeTo(t *testing.T, got, want float64, delta ...float64) {
	t.Helper()
	d := 0.001
	if len(delta) > 0 {
		d = delta[0]
	}
	if math.Abs(got-want) > d {
		t.Errorf("got %v, want %v within %v", got, want, d)
	}
}

// weighted_average_spec.rb

func TestWeightedAverageAveragesProviders(t *testing.T) {
	got := WeightedAverage(plain([]rates.Row{
		row(jan15, "EUR", "USD", 1.08, "ECB"),
		row(jan15, "EUR", "USD", 1.10, "BOC"),
	}), far)
	closeTo(t, findBlended(t, got, "USD").Rate, (1.08+1.10)/2.0)
}

func TestWeightedAverageEqualWithinGrace(t *testing.T) {
	got := WeightedAverage(plain([]rates.Row{
		row(jan15, "EUR", "USD", 1.08, "ECB"),
		row(jan15.AddDate(0, 0, -1), "EUR", "USD", 1.10, "BOC"),
	}), far)
	closeTo(t, findBlended(t, got, "USD").Rate, (1.08+1.10)/2.0)
}

func TestWeightedAveragePicksMostRecentDate(t *testing.T) {
	got := WeightedAverage(plain([]rates.Row{
		row(jan15.AddDate(0, 0, -1), "EUR", "USD", 1.10, "BOC"),
		row(jan15, "EUR", "USD", 1.08, "ECB"),
	}), far)
	if d := findBlended(t, got, "USD").Date; !d.Equal(jan15) {
		t.Errorf("date = %v", d)
	}
}

func TestWeightedAverageDiscountsStaleRates(t *testing.T) {
	got := WeightedAverage(plain([]rates.Row{
		row(jan15, "EUR", "USD", 1.08, "ECB"),
		row(jan15.AddDate(0, 0, -7), "EUR", "USD", 1.20, "BOC"),
	}), far)
	closeTo(t, findBlended(t, got, "USD").Rate, 1.08, 0.02)
}

func TestWeightedAverageExposesProvidersSortedByKey(t *testing.T) {
	got := WeightedAverage(plain([]rates.Row{
		row(jan15, "EUR", "USD", 1.08, "ECB"),
		row(jan15, "EUR", "USD", 1.10, "BOC"),
	}), far)
	want := []currency.Contribution{{Key: "BOC", Date: jan15, Rate: 1.10}, {Key: "ECB", Date: jan15, Rate: 1.08}}
	if p := findBlended(t, got, "USD").Providers; !reflect.DeepEqual(p, want) {
		t.Errorf("providers = %+v", p)
	}
}

func TestWeightedAveragePicksLatestRatePerProvider(t *testing.T) {
	got := WeightedAverage(plain([]rates.Row{
		row(jan15, "EUR", "USD", 1.08, "ECB"),
		row(jan15, "EUR", "USD", 1.10, "BOC"),
		row(jan15.AddDate(0, 0, -1), "EUR", "USD", 1.09, "BOC"),
	}), far)
	want := []currency.Contribution{{Key: "BOC", Date: jan15, Rate: 1.10}, {Key: "ECB", Date: jan15, Rate: 1.08}}
	if p := findBlended(t, got, "USD").Providers; !reflect.DeepEqual(p, want) {
		t.Errorf("providers = %+v", p)
	}
}

func TestWeightedAverageIgnoresFutureRatesForDecay(t *testing.T) {
	today := rates.Today()
	got := WeightedAverage(plain([]rates.Row{
		row(today.AddDate(0, 0, 1), "EUR", "GEL", 3.0, "NBG"),
		row(today, "EUR", "USD", 1.08, "ECB"),
		row(today.AddDate(0, 0, -3), "EUR", "USD", 1.10, "BOC"),
	}), today)
	closeTo(t, findBlended(t, got, "USD").Rate, 1.09, 0.0001)
}

func TestWeightedAverageEmpty(t *testing.T) {
	if got := WeightedAverage(nil, far); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestKBSumMatchesRubyArraySum(t *testing.T) {
	// [0.1] * 10 sums to exactly 1.0 in Ruby; naive addition gives
	// 0.9999999999999999.
	values := make([]float64, 10)
	for i := range values {
		values[i] = 0.1
	}
	if got := kbSum(values); got != 1.0 {
		t.Errorf("kbSum = %v", got)
	}
	if got := kbSum([]float64{3.0, 1e100, -1e100}); got != 3.0 {
		t.Errorf("kbSum = %v", got)
	}
}

// consensus_spec.rb

func build(providers map[string]float64) []rates.Row {
	var out []rates.Row
	for _, p := range []string{"A", "B", "C", "D"} {
		if rate, ok := providers[p]; ok {
			out = append(out, row(far, "EUR", "USD", rate, p))
		}
	}
	return out
}

func has(rows []rates.Row, provider, quote string) bool {
	for _, r := range rows {
		if r.Provider == provider && r.Quote == quote {
			return true
		}
	}
	return false
}

func TestConsensusFiltersDeviatingRate(t *testing.T) {
	rows := build(map[string]float64{"A": 1.10, "B": 1.11, "C": 1.10, "D": 9.99})
	if has(ConsensusFind(rows), "D", "USD") {
		t.Error("find kept D")
	}
	if !has(ConsensusOutliers(rows), "D", "USD") {
		t.Error("outliers miss D")
	}
}

func TestConsensusKeepsRatesWithinConsensus(t *testing.T) {
	rows := build(map[string]float64{"A": 1.10, "B": 1.11, "C": 1.12, "D": 1.105})
	if n := len(ConsensusFind(rows)); n != 4 {
		t.Errorf("find = %d", n)
	}
	if o := ConsensusOutliers(rows); len(o) != 0 {
		t.Errorf("outliers = %+v", o)
	}
}

func TestConsensusSkipsQuotesWithFewProviders(t *testing.T) {
	rows := build(map[string]float64{"A": 1.10, "B": 9.99, "C": 1.11})
	if n := len(ConsensusFind(rows)); n != 3 {
		t.Errorf("find = %d", n)
	}
	if o := ConsensusOutliers(rows); len(o) != 0 {
		t.Errorf("outliers = %+v", o)
	}
}

func TestConsensusToleratesSmallFluctuations(t *testing.T) {
	rows := build(map[string]float64{"A": 612.55, "B": 612.50, "C": 612.60, "D": 610.40})
	if o := ConsensusOutliers(rows); len(o) != 0 {
		t.Errorf("outliers = %+v", o)
	}
}

func TestConsensusFiltersPerQuote(t *testing.T) {
	rows := []rates.Row{
		row(far, "EUR", "USD", 1.17, "A"), row(far, "EUR", "AED", 4.30, "A"),
		row(far, "EUR", "USD", 1.17, "B"), row(far, "EUR", "AED", 4.30, "B"),
		row(far, "EUR", "USD", 1.17, "C"), row(far, "EUR", "AED", 25.8, "C"),
		row(far, "EUR", "USD", 1.17, "D"), row(far, "EUR", "AED", 4.30, "D"),
	}
	outliers, found := ConsensusOutliers(rows), ConsensusFind(rows)
	if !has(outliers, "C", "AED") || has(outliers, "C", "USD") {
		t.Errorf("outliers = %+v", outliers)
	}
	if !has(found, "C", "USD") || has(found, "C", "AED") {
		t.Errorf("find = %+v", found)
	}
}

// blender_spec.rb

func TestBlendAveragesAcrossProviders(t *testing.T) {
	got := Blend([]rates.Row{
		row(jan15, "EUR", "USD", 1.08, "ECB"), row(jan15, "EUR", "GBP", 0.84, "ECB"),
		row(jan15, "EUR", "USD", 1.10, "BOC"), row(jan15, "EUR", "GBP", 0.86, "BOC"),
	}, "EUR", far)
	closeTo(t, findBlended(t, got, "USD").Rate, (1.08+1.10)/2.0)
}

func TestBlendHandlesMixedBasesWithinProvider(t *testing.T) {
	got := Blend([]rates.Row{
		row(jan15, "USD", "JPY", 150.0, "FRED"),
		row(jan15, "USD", "CHF", 0.88, "FRED"),
		row(jan15, "EUR", "USD", 1.10, "FRED"),
	}, "USD", far)
	closeTo(t, findBlended(t, got, "JPY").Rate, 150.0)
	closeTo(t, findBlended(t, got, "EUR").Rate, 1.0/1.10)
}

func TestBlendConsistentAcrossProviderBases(t *testing.T) {
	got := Blend([]rates.Row{
		row(jan15, "EUR", "USD", 1.08, "ECB"),
		row(jan15, "USD", "CAD", 1.37, "BOC"),
		row(jan15, "EUR", "CAD", 1.48, "BOC"),
	}, "EUR", far)
	closeTo(t, findBlended(t, got, "USD").Rate, 1.08, 0.05)
}

func TestBlendEqualWithinGrace(t *testing.T) {
	got := findBlended(t, Blend([]rates.Row{
		row(jan15, "EUR", "USD", 1.08, "ECB"),
		row(jan15.AddDate(0, 0, -1), "EUR", "USD", 1.10, "BOC"),
	}, "EUR", far), "USD")
	closeTo(t, got.Rate, (1.08+1.10)/2.0)
	if !got.Date.Equal(jan15) {
		t.Errorf("date = %v", got.Date)
	}
}

func TestBlendDiscountsStaleRates(t *testing.T) {
	got := findBlended(t, Blend([]rates.Row{
		row(jan15, "EUR", "USD", 1.08, "ECB"),
		row(jan15.AddDate(0, 0, -7), "EUR", "USD", 1.20, "BOC"),
	}, "EUR", far), "USD")
	closeTo(t, got.Rate, 1.08, 0.02)
	if !got.Date.Equal(jan15) {
		t.Errorf("date = %v", got.Date)
	}
}

var outlierRows = []rates.Row{
	row(jan15, "EUR", "USD", 1.08, "A"),
	row(jan15, "EUR", "USD", 1.09, "B"),
	row(jan15, "EUR", "USD", 1.08, "C"),
	row(jan15, "EUR", "USD", 9.99, "D"),
}

func TestBlendExcludesOutliers(t *testing.T) {
	closeTo(t, findBlended(t, Blend(outlierRows, "EUR", far), "USD").Rate, 1.083, 0.01)
	if o := Outliers(outlierRows, "EUR"); len(o) != 1 || o[0].Provider != "D" {
		t.Errorf("outliers = %+v", o)
	}
}

func TestBlendMarksOutliersExcluded(t *testing.T) {
	want := []currency.Contribution{
		{Key: "A", Date: jan15, Rate: 1.08},
		{Key: "B", Date: jan15, Rate: 1.09},
		{Key: "C", Date: jan15, Rate: 1.08},
		{Key: "D", Date: jan15, Rate: 9.99, Excluded: true},
	}
	if p := findBlended(t, Blend(outlierRows, "EUR", far), "USD").Providers; !reflect.DeepEqual(p, want) {
		t.Errorf("providers = %+v", p)
	}
}

// base_conversion_spec.rb

var ecbRates = []rates.Row{
	row(jan15, "EUR", "USD", 1.08, "ECB"),
	row(jan15, "EUR", "GBP", 0.85, "ECB"),
}

func TestConvertToDifferentBase(t *testing.T) {
	got := Convert(ecbRates, "USD")
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	closeTo(t, findRow(t, got, "GBP").Rate, 0.85/1.08)
}

func TestConvertInvertsNativeBase(t *testing.T) {
	closeTo(t, findRow(t, Convert(ecbRates, "USD"), "EUR").Rate, 1.0/1.08)
}

func TestConvertPreservesProvider(t *testing.T) {
	for _, r := range Convert(ecbRates, "USD") {
		if r.Provider != "ECB" {
			t.Errorf("provider = %q", r.Provider)
		}
	}
}

func TestConvertKeepsNativeBaseRates(t *testing.T) {
	if got := findRow(t, Convert(ecbRates, "EUR"), "USD").Rate; got != 1.08 {
		t.Errorf("rate = %v", got)
	}
}

func TestConvertEmptyWithoutBase(t *testing.T) {
	if got := Convert(ecbRates, "JPY"); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestConvertCrossesThroughSharedQuote(t *testing.T) {
	got := Convert([]rates.Row{
		row(jan15, "USD", "CAD", 1.37, "BOC"),
		row(jan15, "EUR", "CAD", 1.48, "BOC"),
		row(jan15, "TRY", "CAD", 0.031, "BOC"),
	}, "EUR")
	closeTo(t, findRow(t, got, "USD").Rate, 1.48/1.37)
	closeTo(t, findRow(t, got, "TRY").Rate, 1.48/0.031)
}

func TestConvertMixedBasesThroughInvertedRows(t *testing.T) {
	got := Convert([]rates.Row{
		row(jan15, "USD", "JPY", 150.0, "FRED"),
		row(jan15, "EUR", "USD", 1.10, "FRED"),
	}, "EUR")
	closeTo(t, findRow(t, got, "JPY").Rate, 150.0*1.10)
	if r := findRow(t, got, "USD").Rate; r != 1.10 {
		t.Errorf("USD = %v", r)
	}
}

func TestConvertBridgeQuotingBase(t *testing.T) {
	got := Convert([]rates.Row{
		row(jan15, "USD", "JPY", 150.0, "FRED"),
		row(jan15, "USD", "EUR", 0.91, "FRED"),
	}, "EUR")
	closeTo(t, findRow(t, got, "JPY").Rate, 150.0/0.91)
}

func TestConvertBridgesThroughFirstMatchingRow(t *testing.T) {
	// Carry-forward snapshots can mix observation dates within a group; the
	// bridge resolves to the first matching row in group order, as Array#find
	// did.
	tuesday := adapter.Date(2024, 1, 16)
	got := Convert([]rates.Row{
		row(jan15, "EUR", "USD", 1.08, "ECB"),
		row(tuesday, "EUR", "USD", 1.10, "ECB"),
		row(tuesday, "GBP", "USD", 1.27, "ECB"),
	}, "EUR")
	closeTo(t, findRow(t, got, "GBP").Rate, 1.08/1.27)
}

func usdRows(rows []rates.Row) []rates.Row {
	var out []rates.Row
	for _, r := range rows {
		if r.Quote == "USD" {
			out = append(out, r)
		}
	}
	return out
}

func TestConvertCollapsesTwoBridges(t *testing.T) {
	usd := usdRows(Convert([]rates.Row{
		row(jan15, "EUR", "USD", 1.16, "IMF"),
		row(jan15, "USD", "XDR", 0.73, "IMF"),
		row(jan15, "EUR", "XDR", 0.85, "IMF"),
	}, "EUR"))
	if len(usd) != 1 {
		t.Fatalf("usd = %+v", usd)
	}
	closeTo(t, usd[0].Rate, (1.16+(0.85/0.73))/2.0)
}

func TestConvertCollapsesDivergentBridges(t *testing.T) {
	usd := usdRows(Convert([]rates.Row{
		row(jan15, "EUR", "USD", 1.16, "IMF"),
		row(jan15, "USD", "XDR", 0.73, "IMF"),
		row(jan15, "EUR", "XDR", 1.50, "IMF"),
	}, "EUR"))
	if len(usd) != 1 {
		t.Fatalf("usd = %+v", usd)
	}
	closeTo(t, usd[0].Rate, (1.16+(1.50/0.73))/2.0)
}

func TestConvertBridgesWithinProviderOnly(t *testing.T) {
	jpy := findRow(t, Convert([]rates.Row{
		row(jan15, "EUR", "USD", 1.08, "ECB"),
		row(jan15, "USD", "JPY", 150.0, "FRED"),
		row(jan15, "EUR", "USD", 1.10, "FRED"),
	}, "EUR"), "JPY")
	// JPY bridged through FRED's EUR/USD (1.10), not ECB's.
	closeTo(t, jpy.Rate, 150.0*1.10)
	if jpy.Provider != "FRED" {
		t.Errorf("provider = %q", jpy.Provider)
	}
}
