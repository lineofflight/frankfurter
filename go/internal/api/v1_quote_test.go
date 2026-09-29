package api

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// spec/versions/v1/quote/base_spec.rb

func baseQuote(fetch func() []rates.Row) *v1Quote {
	q := newV1Quote(v1Query{Date: fixtures.Today()})
	if fetch != nil {
		q.fetch = func(context.Context) ([]rates.Row, error) { return fetch(), nil }
	}
	return &q
}

func TestQuoteRequiresData(t *testing.T) {
	if _, err := baseQuote(nil).Perform(context.Background()); err == nil {
		t.Fatal("want an error without a data source")
	}
}

// Ruby raises NotImplementedError from Base#formatted and #cache_key; in Go only the concrete quotes have them.
func TestQuoteDoesNotKnowHowToFormatOrKey(t *testing.T) {
	var q any = baseQuote(nil)
	if _, ok := q.(interface{ Formatted() any }); ok {
		t.Error("base quote formats")
	}
	if _, ok := q.(interface{ CacheKey() string }); ok {
		t.Error("base quote has a cache key")
	}
}

func TestQuoteDefaults(t *testing.T) {
	q := baseQuote(nil)
	if q.Base != "EUR" {
		t.Errorf("base = %q", q.Base)
	}
	if q.Amount != 1 {
		t.Errorf("amount = %v", q.Amount)
	}
}

func TestQuotePerformsOnlyOnce(t *testing.T) {
	q := baseQuote(func() []rates.Row { return nil })
	if ok, err := q.Perform(context.Background()); !ok || err != nil {
		t.Fatalf("first perform = %v, %v", ok, err)
	}
	if ok, _ := q.Perform(context.Background()); ok {
		t.Fatal("performed twice")
	}
}

func row(quote string, rate float64) rates.Row {
	return rates.Row{Date: fixtures.Today(), Base: "EUR", Quote: quote, Provider: "ECB", Rate: rate}
}

func TestQuoteWithoutRebasingDoesNotRound(t *testing.T) {
	q := baseQuote(func() []rates.Row { return []rates.Row{row("INR", 82.1234)} })
	q.Perform(context.Background())
	if got := q.result.days[0].rates.toMap()["INR"]; got != 82.1234 {
		t.Fatalf("INR = %v", got)
	}
}

func TestQuoteRebasingFromUnavailableCurrencyFindsNothing(t *testing.T) {
	q := baseQuote(func() []rates.Row { return []rates.Row{row("USD", 1)} })
	q.Base = "ILS"
	q.Perform(context.Background())
	if !q.NotFound() {
		t.Fatal("found something")
	}
}

func TestQuoteRebasingToUnavailableCurrencyFindsNothing(t *testing.T) {
	q := baseQuote(func() []rates.Row { return []rates.Row{row("USD", 1)} })
	q.Base, q.Symbols = "USD", []string{"FOO"}
	q.Perform(context.Background())
	if !q.NotFound() {
		t.Fatal("found something")
	}
}

func TestQuoteRebaseRoundsAndSorts(t *testing.T) {
	q := baseQuote(func() []rates.Row { return []rates.Row{row("USD", 1.08), row("GBP", 0.86), row("JPY", 160)} })
	q.Base, q.Amount = "GBP", 100
	q.Perform(context.Background())
	got := q.result.days[0].rates
	// Rates are scaled and rounded first (USD 108, GBP 86, JPY 16000, EUR 100), then divided and rounded again.
	want := quoteRates{{"EUR", 116.28}, {"JPY", 18605}, {"USD", 125.58}}
	if len(got) != len(want) {
		t.Fatalf("rates = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("rates[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

// spec/versions/v1/quote/end_of_day_spec.rb

func performed[Q interface {
	Perform(context.Context) (bool, error)
}](t *testing.T, q Q) Q {
	t.Helper()
	if _, err := q.Perform(context.Background()); err != nil {
		t.Fatal(err)
	}
	return q
}

func endOfDay(t *testing.T, q v1Query) *v1EndOfDay {
	t.Helper()
	q.Date = fixtures.BusinessDay(30)
	return performed(t, newV1EndOfDay(fixtures.New(t), q))
}

func TestEndOfDayReturnsRates(t *testing.T) {
	if len(endOfDay(t, v1Query{}).Formatted().Rates) == 0 {
		t.Fatal("no rates")
	}
}

func TestEndOfDayQuotesGivenDate(t *testing.T) {
	d, err := db.ParseDate(endOfDay(t, v1Query{}).Formatted().Date)
	if err != nil {
		t.Fatal(err)
	}
	if d.After(fixtures.BusinessDay(30)) {
		t.Fatalf("date = %v", d)
	}
}

func TestEndOfDayQuotesAgainstEuro(t *testing.T) {
	if _, ok := endOfDay(t, v1Query{}).Formatted().Rates["EUR"]; ok {
		t.Fatal("EUR quoted")
	}
}

func TestEndOfDaySortsRates(t *testing.T) {
	e := endOfDay(t, v1Query{})
	got := e.result.days[0].rates
	if !sort.SliceIsSorted(got, func(i, j int) bool { return got[i].quote < got[j].quote }) {
		t.Fatalf("rates = %v", got)
	}
}

func TestEndOfDayHasCacheKey(t *testing.T) {
	if endOfDay(t, v1Query{}).CacheKey() == "" {
		t.Fatal("empty cache key")
	}
}

func TestEndOfDayGivenNewBase(t *testing.T) {
	e := endOfDay(t, v1Query{Base: "USD", HasBase: true})
	if _, ok := e.Formatted().Rates["USD"]; ok {
		t.Fatal("USD quoted against itself")
	}
	got := e.result.days[0].rates
	if !sort.SliceIsSorted(got, func(i, j int) bool { return got[i].quote < got[j].quote }) {
		t.Fatalf("rates = %v", got)
	}
}

func TestEndOfDayGivenSymbols(t *testing.T) {
	e := endOfDay(t, v1Query{Symbols: []string{"USD", "GBP", "JPY"}})
	r := e.Formatted().Rates
	if _, ok := r["USD"]; !ok {
		t.Error("no USD")
	}
	if _, ok := r["CAD"]; ok {
		t.Error("CAD quoted")
	}
	got := e.result.days[0].rates
	if !sort.SliceIsSorted(got, func(i, j int) bool { return got[i].quote < got[j].quote }) {
		t.Fatalf("rates = %v", got)
	}
}

func TestEndOfDayGivenAmount(t *testing.T) {
	e := endOfDay(t, v1Query{Amount: 100, HasAmount: true})
	if usd := e.Formatted().Rates["USD"]; !(usd > 10) {
		t.Fatalf("USD = %v", usd)
	}
}

// spec/versions/v1/quote/interval_spec.rb

func intervalDates() (time.Time, time.Time) {
	return fixtures.LatestDate().AddDate(0, 0, -365), fixtures.LatestDate()
}

func interval(t *testing.T, q v1Query) *v1Interval {
	t.Helper()
	q.Start, q.End = intervalDates()
	q.IsInterval = true
	return performed(t, newV1Interval(fixtures.New(t), q, fixtures.Today()))
}

func eachDay(t *testing.T, iv *v1Interval, check func(label string, r quoteRates)) {
	t.Helper()
	if len(iv.result.days) == 0 {
		t.Fatal("no dates")
	}
	for _, d := range iv.result.days {
		check(d.date, d.rates)
	}
}

func checkSorted(t *testing.T, iv *v1Interval) {
	t.Helper()
	eachDay(t, iv, func(label string, r quoteRates) {
		if !sort.SliceIsSorted(r, func(i, j int) bool { return r[i].quote < r[j].quote }) {
			t.Errorf("%s: rates = %v", label, r)
		}
	})
}

func TestIntervalReturnsRates(t *testing.T) {
	if len(interval(t, v1Query{}).Formatted().Rates) == 0 {
		t.Fatal("no rates")
	}
}

func TestIntervalQuotesGivenDateInterval(t *testing.T) {
	f := interval(t, v1Query{}).Formatted()
	start, end := intervalDates()
	gotStart, _ := db.ParseDate(f.StartDate)
	gotEnd, _ := db.ParseDate(f.EndDate)
	// The returned start date is the closest working day on or before the requested start, but not far back.
	if gotStart.After(start) || !gotStart.After(start.AddDate(0, 0, -10)) {
		t.Errorf("start_date = %s", f.StartDate)
	}
	if gotEnd.After(end) {
		t.Errorf("end_date = %s", f.EndDate)
	}
}

func TestIntervalQuotesAgainstEuro(t *testing.T) {
	eachDay(t, interval(t, v1Query{}), func(label string, r quoteRates) {
		if _, ok := r.toMap()["EUR"]; ok {
			t.Errorf("%s: EUR quoted", label)
		}
	})
}

func TestIntervalSortsRates(t *testing.T) {
	checkSorted(t, interval(t, v1Query{}))
}

func TestIntervalHasCacheKey(t *testing.T) {
	if interval(t, v1Query{}).CacheKey() == "" {
		t.Fatal("empty cache key")
	}
}

func TestIntervalGivenNewBase(t *testing.T) {
	iv := interval(t, v1Query{Base: "USD", HasBase: true})
	eachDay(t, iv, func(label string, r quoteRates) {
		if _, ok := r.toMap()["USD"]; ok {
			t.Errorf("%s: USD quoted against itself", label)
		}
	})
	checkSorted(t, iv)
}

func TestIntervalGivenSymbols(t *testing.T) {
	iv := interval(t, v1Query{Symbols: []string{"USD", "GBP", "JPY"}})
	eachDay(t, iv, func(label string, r quoteRates) {
		m := r.toMap()
		if _, ok := m["USD"]; !ok {
			t.Errorf("%s: no USD", label)
		}
		if _, ok := m["CAD"]; ok {
			t.Errorf("%s: CAD quoted", label)
		}
	})
	checkSorted(t, iv)
}

func TestIntervalGivenAmount(t *testing.T) {
	eachDay(t, interval(t, v1Query{Amount: 100, HasAmount: true}), func(label string, r quoteRates) {
		if usd := r.toMap()["USD"]; !(usd > 10) {
			t.Errorf("%s: USD = %v", label, usd)
		}
	})
}

// spec/versions/v1/currency_names_spec.rb

func TestCurrencyNamesReturnsCodesAndNames(t *testing.T) {
	c, err := loadV1CurrencyNames(context.Background(), fixtures.New(t), fixtures.Today())
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Formatted()["USD"]; got != "United States Dollar" {
		t.Fatalf("USD = %q", got)
	}
}

func TestCurrencyNamesHasCacheKey(t *testing.T) {
	c, err := loadV1CurrencyNames(context.Background(), fixtures.New(t), fixtures.Today())
	if err != nil {
		t.Fatal(err)
	}
	if c.CacheKey() == "" {
		t.Fatal("empty cache key")
	}
}

func TestCurrencyNamesOmitsUnknownAndExpiredCodes(t *testing.T) {
	ctx := context.Background()
	conn := fixtures.New(t)
	for _, quote := range []string{"ZZZ", "SLL"} {
		if _, err := conn.ExecContext(ctx, "INSERT INTO rates (provider, date, base, quote, mid) VALUES ('ECB', ?, 'EUR', ?, 2.0)",
			db.FormatDate(fixtures.LatestDate()), quote); err != nil {
			t.Fatal(err)
		}
	}
	c, err := loadV1CurrencyNames(ctx, conn, fixtures.Today())
	if err != nil {
		t.Fatal(err)
	}
	names := c.Formatted()
	for _, code := range []string{"ZZZ", "SLL"} {
		if _, ok := names[code]; ok {
			t.Errorf("%s listed", code)
		}
	}
	if names["USD"] != "United States Dollar" {
		t.Errorf("USD = %q", names["USD"])
	}
}

// spec/versions/v1/roundable_spec.rb (Roundable lives in internal/rates as rates.Round)

func TestRoundable(t *testing.T) {
	for _, c := range []struct {
		name     string
		in, want float64
	}{
		{"over 5,000 to zero places", 5000.123456, 5000},
		{"over 80 to two places", 80.123456, 80.12},
		{"below 5,000 to two places", 4999.123456, 4999.12},
		{"below 80 to three places", 79.123456, 79.123},
		{"over 20 to three places", 20.123456, 20.123},
		{"below 20 to four places", 19.123456, 19.1235},
		{"over 1 to four places", 1.123456, 1.1235},
		{"below 1 to five places", 0.123456, 0.12346},
		{"below 0.0001 to six places", 0.0000655, 0.000066},
	} {
		if got := rates.Round(c.in); got != c.want {
			t.Errorf("%s: Round(%v) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}
