package rates_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	_ "github.com/lineofflight/frankfurter/go/internal/adapters/hmrc" // registers providers with a lead
	_ "github.com/lineofflight/frankfurter/go/internal/adapters/jpc"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

func d(y int, m time.Month, day int) time.Time { return adapter.Date(y, m, day) }

func codes(records []adapter.Rate) [][2]string {
	out := make([][2]string, len(records))
	for i, r := range records {
		out[i] = [2]string{r.Base, r.Quote}
	}
	return out
}

func quotesOf(records []adapter.Rate) []string {
	out := make([]string, len(records))
	for i, r := range records {
		out[i] = r.Quote
	}
	return out
}

func TestRejectKeepsUnrecognisedCodes(t *testing.T) {
	today := fixtures.Today()
	got := rates.Reject([]adapter.Rate{
		{Date: today, Base: "EUR", Quote: "USD", Rate: 1.1},
		{Date: today, Base: "EUR", Quote: "SDR", Rate: 1.5},
		{Date: today, Base: "SDR", Quote: "USD", Rate: 1.2},
	}, 0, today)
	if want := [][2]string{{"EUR", "USD"}, {"EUR", "SDR"}, {"SDR", "USD"}}; !reflect.DeepEqual(codes(got), want) {
		t.Errorf("got %v", codes(got))
	}
}

// Ruby's nil rate is NaN here: adapter.Rate.Rate is a float64.
func TestRejectDropsNonPositiveRates(t *testing.T) {
	today := fixtures.Today()
	got := rates.Reject([]adapter.Rate{
		{Date: today, Base: "EUR", Quote: "USD", Rate: 1.1},
		{Date: today, Base: "EUR", Quote: "GBP", Rate: 0.0},
		{Date: today, Base: "EUR", Quote: "JPY", Rate: -1.0},
		{Date: today, Base: "EUR", Quote: "CAD", Rate: math.NaN()},
	}, 0, today)
	if !reflect.DeepEqual(quotesOf(got), []string{"USD"}) {
		t.Errorf("got %v", quotesOf(got))
	}
}

func TestRejectDropsBeyondHorizonKeepsGrace(t *testing.T) {
	today := fixtures.Today()
	got := rates.Reject([]adapter.Rate{
		{Date: today.AddDate(0, 0, 1), Base: "EUR", Quote: "USD", Rate: 1.1},
		{Date: today.AddDate(0, 0, 365), Base: "EUR", Quote: "GBP", Rate: 0.85},
	}, 0, today)
	if !reflect.DeepEqual(quotesOf(got), []string{"USD"}) {
		t.Errorf("got %v", quotesOf(got))
	}
}

func TestRejectHorizonIsInclusive(t *testing.T) {
	today := fixtures.Today()
	for _, lead := range []int{0, 31} {
		got := rates.Reject([]adapter.Rate{
			{Date: today.AddDate(0, 0, rates.MaxFutureDrift+lead), Base: "EUR", Quote: "USD", Rate: 1.1},
			{Date: today.AddDate(0, 0, rates.MaxFutureDrift+lead+1), Base: "EUR", Quote: "GBP", Rate: 0.85},
		}, lead, today)
		if !reflect.DeepEqual(quotesOf(got), []string{"USD"}) {
			t.Errorf("lead %d: got %v", lead, quotesOf(got))
		}
	}
}

func TestRejectKeepsRowWithinLead(t *testing.T) {
	today := fixtures.Today()
	got := rates.Reject([]adapter.Rate{{Date: today.AddDate(0, 0, 14), Base: "GBP", Quote: "USD", Rate: 1.3}}, 31, today)
	if len(got) != 1 {
		t.Errorf("got %v", got)
	}
}

func TestRejectKeepsDefunctCurrencyAfterTerminalDate(t *testing.T) {
	got := rates.Reject([]adapter.Rate{
		{Date: d(2016, 7, 1), Base: "EUR", Quote: "BYR", Rate: 22000.0},
		{Date: d(2016, 6, 30), Base: "EUR", Quote: "BYR", Rate: 22000.0},
		{Date: d(2016, 7, 1), Base: "EUR", Quote: "USD", Rate: 1.1},
	}, 0, fixtures.Today())
	if len(got) != 3 {
		t.Errorf("got %d", len(got))
	}
}

func TestRejectMapsPreEuroQuotesToXEU(t *testing.T) {
	// The euro came into existence on 1999-01-01. The Riksbank backfills its EUR series with the ECU back to 1993;
	// relaying those as EUR fabricates euro quotes for dates the euro did not exist.
	got := rates.Reject([]adapter.Rate{
		{Date: d(1998, 12, 31), Base: "SEK", Quote: "EUR", Rate: 0.10448},
		{Date: d(1999, 1, 4), Base: "SEK", Quote: "EUR", Rate: 0.10500},
	}, 0, fixtures.Today())
	if !reflect.DeepEqual(quotesOf(got), []string{"XEU", "EUR"}) || got[0].Rate != 0.10448 || got[1].Rate != 0.10500 {
		t.Errorf("got %+v", got)
	}
}

func TestRejectMapsPrematureBase(t *testing.T) {
	got := rates.Reject([]adapter.Rate{{Date: d(1998, 12, 31), Base: "EUR", Quote: "USD", Rate: 1.1}}, 0, fixtures.Today())
	if want := []adapter.Rate{{Date: d(1998, 12, 31), Base: "XEU", Quote: "USD", Rate: 1.1}}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestRejectKeepsEurosFirstDay(t *testing.T) {
	in := []adapter.Rate{{Date: d(1999, 1, 1), Base: "EUR", Quote: "USD", Rate: 1.16675}}
	want := append([]adapter.Rate(nil), in...)
	if got := rates.Reject(in, 0, fixtures.Today()); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestRejectKeepsSchillingAfterChangeover(t *testing.T) {
	// ATS was irrevocably fixed to the euro in 1999 and ceased to be legal tender on 2002-02-28. Providers keep
	// publishing stale ATS reference rates years later (AMCM into 2004); they are kept at ingest and capped by scopes.
	got := rates.Reject([]adapter.Rate{
		{Date: d(2002, 3, 1), Base: "EUR", Quote: "ATS", Rate: 13.7603},
		{Date: d(2002, 2, 28), Base: "EUR", Quote: "ATS", Rate: 13.7603},
	}, 0, fixtures.Today())
	if len(got) != 2 {
		t.Errorf("got %d", len(got))
	}
}

// Ruby's "accepts a string date" has no Go counterpart, since adapter.Rate.Date is a time.Time; a recent date is
// simply kept.
func TestRejectKeepsRecentDate(t *testing.T) {
	today := fixtures.Today()
	if got := rates.Reject([]adapter.Rate{{Date: today.AddDate(0, 0, -1), Base: "EUR", Quote: "USD", Rate: 1.1}}, 0, today); len(got) != 1 {
		t.Errorf("got %d", len(got))
	}
}

func countWhere(t *testing.T, conn *sql.DB, table, cond string, args ...any) int {
	t.Helper()
	var n int
	if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM "+table+" WHERE "+cond, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func purge(t *testing.T, conn *sql.DB, today time.Time) rates.PurgeTotals {
	t.Helper()
	leads, err := rates.ProviderLeads(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	totals, err := rates.Purge(ctx, conn, today, leads)
	if err != nil {
		t.Fatal(err)
	}
	return totals
}

func TestProviderLeadsFromRegisteredAdapters(t *testing.T) {
	leads, err := rates.ProviderLeads(ctx, fixtures.New(t))
	if err != nil {
		t.Fatal(err)
	}
	if leads["JPC"] != 7 || leads["HMRC"] != 31 || leads["ECB"] != 0 {
		t.Errorf("leads = %v", leads)
	}
}

func TestPurgeDeletesFutureRowsAcrossTables(t *testing.T) {
	conn := fixtures.New(t)
	today := fixtures.Today()
	future := db.FormatDate(today.AddDate(0, 0, 365))
	exec(t, conn, `INSERT INTO rates (provider, date, base, quote, mid) VALUES
		('TEST', ?, 'EUR', 'USD', 1.1), ('TEST', ?, 'EUR', 'USD', 1.1)`, future, db.FormatDate(today))
	exec(t, conn, "INSERT INTO weekly_rates (provider, bucket_date, base, quote, rate) VALUES ('TEST', ?, 'EUR', 'USD', 1.1)", future)
	exec(t, conn, "INSERT INTO monthly_rates (provider, bucket_date, base, quote, rate) VALUES ('TEST', ?, 'EUR', 'USD', 1.1)", future)

	purge(t, conn, today)

	if n := countWhere(t, conn, "rates", "provider = 'TEST' AND date = ?", future); n != 0 {
		t.Errorf("future rates = %d", n)
	}
	if n := countWhere(t, conn, "rates", "provider = 'TEST' AND date = ?", db.FormatDate(today)); n != 1 {
		t.Errorf("today's rates = %d", n)
	}
	if n := countWhere(t, conn, "weekly_rates", "provider = 'TEST' AND bucket_date = ?", future); n != 0 {
		t.Errorf("future weekly = %d", n)
	}
	if n := countWhere(t, conn, "monthly_rates", "provider = 'TEST' AND bucket_date = ?", future); n != 0 {
		t.Errorf("future monthly = %d", n)
	}
}

func TestPurgeKeepsForwardRowsOfProviderPublishingAhead(t *testing.T) {
	conn := fixtures.New(t)
	today := d(2026, 9, 11)
	ahead := db.FormatDate(today.AddDate(0, 0, 14))
	bucket := db.FormatDate(rates.Bucket(rates.Month, today.AddDate(0, 1, 0)))
	exec(t, conn, `INSERT INTO rates (provider, date, base, quote, mid) VALUES
		('HMRC', ?, 'GBP', 'USD', 1.3), ('TEST', ?, 'EUR', 'USD', 1.1)`, ahead, ahead)
	exec(t, conn, `INSERT INTO monthly_rates (provider, bucket_date, base, quote, rate) VALUES
		('HMRC', ?, 'GBP', 'USD', 1.3), ('TEST', ?, 'EUR', 'USD', 1.1)`, bucket, bucket)

	purge(t, conn, today)

	if got := providersAt(t, conn, "SELECT provider FROM rates WHERE date = ? AND provider IN ('HMRC', 'TEST')", ahead); !reflect.DeepEqual(got, []string{"HMRC"}) {
		t.Errorf("rates providers = %v", got)
	}
	if got := providersAt(t, conn, "SELECT provider FROM monthly_rates WHERE bucket_date = ? AND provider IN ('HMRC', 'TEST')", bucket); !reflect.DeepEqual(got, []string{"HMRC"}) {
		t.Errorf("monthly providers = %v", got)
	}
}

func providersAt(t *testing.T, conn *sql.DB, query string, args ...any) []string {
	t.Helper()
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		rows.Scan(&p)
		out = append(out, p)
	}
	return out
}

func TestPurgeRetainsTerminalDateRows(t *testing.T) {
	conn := fixtures.New(t)
	exec(t, conn, `INSERT INTO rates (provider, date, base, quote, mid) VALUES
		('TEST', '2016-07-01', 'USD', 'BYR', 22000.0), ('TEST', '2017-01-01', 'BYR', 'USD', 0.00005),
		('TEST', '2016-06-30', 'USD', 'BYR', 22000.0), ('TEST', '2016-07-01', 'EUR', 'USD', 1.1)`)

	totals := purge(t, conn, fixtures.Today())

	if totals.Rates != 0 {
		t.Errorf("rates purged = %d", totals.Rates)
	}
	for _, c := range []string{
		"quote = 'BYR' AND date = '2016-07-01'", "base = 'BYR' AND date = '2017-01-01'",
		"quote = 'BYR' AND date = '2016-06-30'", "base = 'EUR' AND date = '2016-07-01'",
	} {
		if n := countWhere(t, conn, "rates", c); n != 1 {
			t.Errorf("%s: %d", c, n)
		}
	}
}

func TestPurgeRetainsPrematureCodes(t *testing.T) {
	conn := fixtures.New(t)
	exec(t, conn, `INSERT INTO rates (provider, date, base, quote, mid) VALUES
		('TEST', '1998-12-31', 'SEK', 'EUR', 0.10448), ('TEST', '1999-01-04', 'SEK', 'EUR', 0.10500)`)

	purge(t, conn, fixtures.Today())

	for _, day := range []string{"1998-12-31", "1999-01-04"} {
		if n := countWhere(t, conn, "rates", "provider = 'TEST' AND quote = 'EUR' AND date = ?", day); n != 1 {
			t.Errorf("%s: %d", day, n)
		}
	}
}

func TestPurgeRetainsRollupsPastTerminalDate(t *testing.T) {
	conn := fixtures.New(t)
	exec(t, conn, `INSERT INTO weekly_rates (provider, bucket_date, base, quote, rate) VALUES
		('TEST', '2016-07-04', 'USD', 'BYR', 22000.0), ('TEST', '2016-06-27', 'USD', 'BYR', 22000.0)`)
	exec(t, conn, `INSERT INTO monthly_rates (provider, bucket_date, base, quote, rate) VALUES
		('TEST', '2016-08-01', 'USD', 'BYR', 22000.0), ('TEST', '2016-06-01', 'USD', 'BYR', 22000.0)`)

	totals := purge(t, conn, fixtures.Today())

	if totals.Weekly != 0 || totals.Monthly != 0 {
		t.Errorf("totals = %+v", totals)
	}
	if n := countWhere(t, conn, "weekly_rates", "quote = 'BYR' AND bucket_date = '2016-06-27'"); n != 1 {
		t.Errorf("weekly = %d", n)
	}
	if n := countWhere(t, conn, "monthly_rates", "quote = 'BYR' AND bucket_date = '2016-06-01'"); n != 1 {
		t.Errorf("monthly = %d", n)
	}
}

func TestPurgeKeepsCurrentPeriodRollup(t *testing.T) {
	// Weekly and monthly buckets anchor to a fixed weekday or the first of the month, so the live period's bucket can
	// sit a few days ahead of the latest date it summarises. The daily horizon must not purge it. Ruby stubs the
	// horizon to Monday 2026-06-15; today is the horizon minus the drift.
	conn := fixtures.New(t)
	today := d(2026, 6, 15).AddDate(0, 0, -rates.MaxFutureDrift)
	exec(t, conn, `INSERT INTO weekly_rates (provider, bucket_date, base, quote, rate) VALUES
		('TEST', '2026-06-18', 'EUR', 'USD', 1.1), ('TEST', '2026-06-25', 'EUR', 'USD', 1.1)`)
	exec(t, conn, `INSERT INTO monthly_rates (provider, bucket_date, base, quote, rate) VALUES
		('TEST', '2026-06-01', 'EUR', 'USD', 1.1), ('TEST', '2026-07-01', 'EUR', 'USD', 1.1)`)

	purge(t, conn, today)

	for _, c := range []struct {
		table, bucket string
		want          int
	}{
		{"weekly_rates", "2026-06-18", 1}, {"weekly_rates", "2026-06-25", 0},
		{"monthly_rates", "2026-06-01", 1}, {"monthly_rates", "2026-07-01", 0},
	} {
		if n := countWhere(t, conn, c.table, "provider = 'TEST' AND bucket_date = ?", c.bucket); n != c.want {
			t.Errorf("%s %s = %d, want %d", c.table, c.bucket, n, c.want)
		}
	}
}

func TestPurgeKeepsNonDailyOnlyCurrencyOutOfCatalogue(t *testing.T) {
	conn := fixtures.New(t)
	exec(t, conn, "INSERT INTO providers (key, name, frequency) VALUES ('TST', 'Test', 'monthly')")
	exec(t, conn, `INSERT INTO rates (provider, date, base, quote, mid) VALUES
		('TST', '2016-06-30', 'USD', 'BYR', 22000.0), ('TST', ?, 'USD', 'BYR', 22000.0)`,
		db.FormatDate(fixtures.Today().AddDate(0, 0, 365)))
	exec(t, conn, "DELETE FROM currencies WHERE iso_code = 'BYR'")
	exec(t, conn, "DELETE FROM currency_coverages WHERE iso_code = 'BYR'")

	purge(t, conn, fixtures.Today())

	if n := countWhere(t, conn, "currencies", "iso_code = 'BYR'"); n != 0 {
		t.Errorf("currencies = %d", n)
	}
	if n := countWhere(t, conn, "currency_coverages", "iso_code = 'BYR' AND provider_key = 'TST'"); n != 1 {
		t.Errorf("coverages = %d", n)
	}
}

func TestPurgeRefreshesSummaries(t *testing.T) {
	conn := fixtures.New(t)
	exec(t, conn, `INSERT INTO rates (provider, date, base, quote, mid) VALUES
		('TEST', '2016-06-30', 'USD', 'BYR', 22000.0), ('TEST', ?, 'USD', 'BYR', 22000.0)`,
		db.FormatDate(fixtures.Today().AddDate(0, 0, 365)))
	exec(t, conn, "DELETE FROM currencies WHERE iso_code = 'BYR'")
	exec(t, conn, "INSERT INTO currencies (iso_code, start_date, end_date) VALUES ('BYR', '2016-06-30', '2017-01-01')")
	exec(t, conn, "DELETE FROM currency_coverages WHERE iso_code = 'BYR'")
	exec(t, conn, `INSERT INTO currency_coverages (provider_key, iso_code, start_date, end_date)
		VALUES ('TEST', 'BYR', '2016-06-30', '2017-01-01')`)

	purge(t, conn, fixtures.Today())

	var currencyEnd, coverageEnd string
	if err := conn.QueryRow("SELECT date(end_date) FROM currencies WHERE iso_code = 'BYR'").Scan(&currencyEnd); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow("SELECT date(end_date) FROM currency_coverages WHERE iso_code = 'BYR' AND provider_key = 'TEST'").Scan(&coverageEnd); err != nil {
		t.Fatal(err)
	}
	if currencyEnd != "2016-06-30" || coverageEnd != "2016-06-30" {
		t.Errorf("currency end %s, coverage end %s", currencyEnd, coverageEnd)
	}
}

func TestPurgeRemovesCurrencyRowsWithoutSurvivingRates(t *testing.T) {
	conn := fixtures.New(t)
	exec(t, conn, "INSERT INTO rates (provider, date, base, quote, mid) VALUES ('TEST', ?, 'USD', 'BYR', 22000.0)",
		db.FormatDate(fixtures.Today().AddDate(0, 0, 365)))
	exec(t, conn, "DELETE FROM currencies WHERE iso_code = 'BYR'")
	exec(t, conn, "INSERT INTO currencies (iso_code, start_date, end_date) VALUES ('BYR', '2017-01-01', '2017-01-01')")
	exec(t, conn, "DELETE FROM currency_coverages WHERE iso_code = 'BYR'")
	exec(t, conn, `INSERT INTO currency_coverages (provider_key, iso_code, start_date, end_date)
		VALUES ('TEST', 'BYR', '2017-01-01', '2017-01-01')`)

	purge(t, conn, fixtures.Today())

	if n := countWhere(t, conn, "currencies", "iso_code = 'BYR'"); n != 0 {
		t.Errorf("currencies = %d", n)
	}
	if n := countWhere(t, conn, "currency_coverages", "iso_code = 'BYR'"); n != 0 {
		t.Errorf("coverages = %d", n)
	}
}
