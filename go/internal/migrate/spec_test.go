package migrate

import (
	"context"
	"database/sql"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/blend"
	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/provider"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// The Ruby migration specs run each step in a child process against a scratch
// database; here each test gets its own database file. Ruby's db:setup is Up
// plus the provider seed. The specs also stub Cache.purge to fail, to show
// setup never needs the CDN; Go's migrations and seed have no cache dependency
// at all.

func setup(t *testing.T, conn *sql.DB) {
	t.Helper()
	ctx := context.Background()
	if err := Up(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if err := rates.SeedProviders(ctx, conn); err != nil {
		t.Fatal(err)
	}
}

func mustExec(t *testing.T, q db.Querier, query string, args ...any) {
	t.Helper()
	if _, err := q.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func count(t *testing.T, q db.Querier, query string, args ...any) int {
	t.Helper()
	var n int
	if err := q.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// rate is a rates row to insert; nil components are NULL.
type rate struct {
	provider, date, base, quote string
	mid, bid, ask               *float64
}

func f(v float64) *float64 { return &v }

func insertRates(t *testing.T, q db.Querier, rows ...rate) {
	t.Helper()
	for _, r := range rows {
		mustExec(t, q, "INSERT INTO rates (provider, date, base, quote, mid, bid, ask) VALUES (?, ?, ?, ?, ?, ?, ?)",
			r.provider, r.date, r.base, r.quote, r.mid, r.bid, r.ask)
	}
}

// storedRow is a whole rates row, the generated rate included.
type storedRow struct {
	Provider, Date, Base, Quote string
	Mid, Bid, Ask, Rate         sql.NullFloat64
}

func allRates(t *testing.T, q db.Querier) []storedRow {
	t.Helper()
	rows, err := q.QueryContext(context.Background(), "SELECT provider, date(date), base, quote, mid, bid, ask, rate "+
		"FROM rates ORDER BY provider, date, base, quote")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []storedRow
	for rows.Next() {
		var r storedRow
		if err := rows.Scan(&r.Provider, &r.Date, &r.Base, &r.Quote, &r.Mid, &r.Bid, &r.Ask, &r.Rate); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

type blendRow struct {
	Date, Quote string
	Rate        float64
}

// blendRows reads a blended table in primary key order (quote, then date).
func blendRows(t *testing.T, q db.Querier, table, where string) []blendRow {
	t.Helper()
	col := "bucket_date"
	if table == "blended_rates" {
		col = "date"
	}
	if where != "" {
		where = " WHERE " + where
	}
	rows, err := q.QueryContext(context.Background(), "SELECT date("+col+"), quote, rate FROM "+table+where+
		" ORDER BY quote, "+col)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []blendRow
	for rows.Next() {
		var r blendRow
		if err := rows.Scan(&r.Date, &r.Quote, &r.Rate); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func findProvider(t *testing.T, q db.Querier, key string) provider.Provider {
	t.Helper()
	p, err := provider.Find(context.Background(), q, key)
	if err != nil || p == nil {
		t.Fatalf("provider %s: %v", key, err)
	}
	return *p
}

func unknown(t *testing.T, q db.Querier, key string) []string {
	t.Helper()
	codes, err := findProvider(t, q, key).UnknownCurrencies(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	return codes
}

func dailyReady(t *testing.T, q db.Querier) bool {
	t.Helper()
	ok, err := blend.DailyReady(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func groupedReady(t *testing.T, q db.Querier, r blend.Rollup) bool {
	t.Helper()
	ok, err := r.Ready(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func rebuildBlends(t *testing.T, conn *sql.DB, today time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := blend.RebuildDaily(ctx, conn, today); err != nil {
		t.Fatal(err)
	}
	for _, r := range blend.Rollups {
		if err := r.Rebuild(ctx, conn, today); err != nil {
			t.Fatal(err)
		}
	}
}

func populate(t *testing.T, conn *sql.DB, today time.Time) {
	t.Helper()
	for _, r := range blend.Rollups {
		if _, err := r.Populate(context.Background(), conn, today); err != nil {
			t.Fatal(err)
		}
	}
}

func bucket(p rates.Precision, date string) string {
	d, err := db.ParseDate(date)
	if err != nil {
		panic(err)
	}
	return db.FormatDate(rates.Bucket(p, d))
}

// spec/currency_exclusions_migration_spec.rb
func TestCurrencyExclusionsMigrationIndexesUnknownCodesAndRemainsReversible(t *testing.T) {
	conn, _ := empty(t)
	migrateTo(t, conn, 33)
	insertRates(t, conn,
		rate{provider: "ECB", date: "2000-01-03", base: "USD", quote: "SDR", mid: f(3.0)},
		rate{provider: "ECB", date: "2000-01-04", base: "SDR", quote: "EUR", mid: f(0.3)},
		rate{provider: "BOC", date: "2000-01-05", base: "USD", quote: "EUR", mid: f(0.9)},
		rate{provider: "BOC", date: "2000-01-05", base: "USD", quote: "GHC", mid: f(2.0)},
		rate{provider: "BOC", date: "2000-01-05", base: "GHC", quote: "USD", mid: f(0.5)},
	)
	migrateTo(t, conn, Latest())

	if count(t, conn, "SELECT count(*) FROM sqlite_master WHERE name = 'currency_exclusions'") != 1 {
		t.Fatal("missing exclusions table")
	}
	var code, start, end string
	if count(t, conn, "SELECT count(*) FROM currency_exclusions") != 1 {
		t.Fatal("wrong exclusions")
	}
	if err := conn.QueryRow("SELECT iso_code, date(start_date), date(end_date) FROM currency_exclusions").
		Scan(&code, &start, &end); err != nil {
		t.Fatal(err)
	}
	if code != "SDR" {
		t.Fatalf("wrong exclusions: %s", code)
	}
	if start != "2000-01-03" || end != "2000-01-04" {
		t.Fatalf("wrong dates: %s..%s", start, end)
	}

	migrateTo(t, conn, 33)
	if count(t, conn, "SELECT count(*) FROM sqlite_master WHERE name = 'currency_exclusions'") != 0 {
		t.Fatal("rollback failed")
	}
	if count(t, conn, "SELECT count(*) FROM rates") != 5 {
		t.Fatal("lost rates")
	}
}

// spec/rate_components_migration_spec.rb
func TestRateComponentMigrationsKeepRatesReadableThroughRollbackAndReapplication(t *testing.T) {
	conn, path := empty(t)
	migrateTo(t, conn, Latest())
	insertRates(t, conn,
		rate{provider: "TST", date: "2026-09-01", base: "USD", quote: "EUR", mid: f(100), bid: f(99), ask: f(103)},
		rate{provider: "TST", date: "2026-09-01", base: "USD", quote: "GBP", bid: f(1830.59054685), ask: f(1831.5063)},
		rate{provider: "BOJA", date: "2026-09-01", base: "USD", quote: "JPY", bid: f(0), ask: f(103)},
	)
	read := func(q db.Querier) []float64 {
		rows, err := q.QueryContext(context.Background(), "SELECT rate FROM rates ORDER BY quote")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []float64
		for rows.Next() {
			var v float64
			if err := rows.Scan(&v); err != nil {
				t.Fatal(err)
			}
			out = append(out, v)
		}
		return out
	}
	expected := read(conn)
	if len(expected) != 3 {
		t.Fatalf("resolved rates %v", expected)
	}

	for _, v := range []int{32, 30, 33} {
		migrateTo(t, conn, v)
		if got := version(t, conn); got != v {
			t.Fatalf("wrong migration version %d, want %d", got, v)
		}
		// A standalone connection must read the schema without application
		// callbacks.
		standalone, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		actual := read(standalone)
		standalone.Close()
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("rates changed at version %d: %v, want %v", v, actual, expected)
		}
	}
}

// spec/comesa_dollar_migration_spec.rb
func TestComesaDollarMigrationPromotesStoredRBMRates(t *testing.T) {
	ctx := context.Background()
	today := rates.Today()
	conn, _ := empty(t)

	// prepare
	migrateTo(t, conn, 35)
	mustExec(t, conn, "INSERT INTO providers (key, name, pivot_currency, frequency) VALUES "+
		"('RBM', 'Reserve Bank of Malawi', 'MWK', 'daily')")
	date := "2024-01-02"
	week, month := bucket(rates.Week, date), bucket(rates.Month, date)
	insertRates(t, conn,
		rate{provider: "RBM", date: date, base: "CMD", quote: "MWK", mid: f(100.0)},
		rate{provider: "RBM", date: date, base: "USD", quote: "MWK", mid: f(90.0)},
	)
	mustExec(t, conn, "INSERT INTO currency_exclusions (provider_key, iso_code, start_date, end_date) VALUES "+
		"('RBM', 'CMD', ?, ?)", date, date)
	for table, b := range map[string]string{"weekly_rates": week, "monthly_rates": month} {
		mustExec(t, conn, "INSERT INTO "+table+" (provider, bucket_date, base, quote, rate) VALUES "+
			"('RBM', ?, 'CMD', 'MWK', 100.0), ('RBM', ?, 'USD', 'MWK', 90.0)", b, b)
	}
	mustExec(t, conn, "INSERT INTO blended_rates (date, quote, rate) VALUES (?, 'EUR', 0.9)", date)
	mustExec(t, conn, "INSERT INTO blended_weekly_rates (bucket_date, quote, rate) VALUES (?, 'EUR', 0.9)", week)
	mustExec(t, conn, "INSERT INTO blended_monthly_rates (bucket_date, quote, rate) VALUES (?, 'EUR', 0.9)", month)

	// repair
	setup(t, conn)
	if err := CheckCurrent(ctx, conn); err != nil {
		t.Fatal(err)
	}
	// A second startup must also succeed while blends await the scheduler.
	setup(t, conn)

	if count(t, conn, "SELECT count(*) FROM currency_exclusions WHERE provider_key = 'RBM' AND iso_code = 'CMD'") != 0 {
		t.Fatal("CMD still excluded")
	}
	if count(t, conn, "SELECT count(*) FROM currency_coverages WHERE provider_key = 'RBM' AND iso_code = 'CMD'") != 1 {
		t.Fatal("CMD missing coverage")
	}
	if count(t, conn, "SELECT count(*) FROM currencies WHERE iso_code = 'CMD'") != 1 {
		t.Fatal("CMD missing catalogue entry")
	}
	if info, ok := currency.Find("CMD"); !ok || info.Name != "COMESA Dollar" || info.ISONumeric != "" {
		t.Fatalf("wrong CMD metadata: %+v", info)
	}
	if slices.Contains(unknown(t, conn, "RBM"), "CMD") {
		t.Fatal("CMD still fails provider health")
	}
	if dailyReady(t, conn) {
		t.Fatal("daily blend stayed ready")
	}
	if count(t, conn, "SELECT count(*) FROM blended_rates") != 0 {
		t.Fatal("daily blend stayed materialized")
	}
	if count(t, conn, "SELECT count(*) FROM blended_weekly_rates WHERE bucket_date = ?", week) != 0 {
		t.Fatal("weekly CMD bucket stayed materialized")
	}
	if count(t, conn, "SELECT count(*) FROM blended_monthly_rates WHERE bucket_date = ?", month) != 0 {
		t.Fatal("monthly CMD bucket stayed materialized")
	}

	if err := blend.RebuildDaily(ctx, conn, today); err != nil {
		t.Fatal(err)
	}
	populate(t, conn, today)
	if count(t, conn, "SELECT count(*) FROM blended_rates WHERE date = ? AND quote = 'CMD'", date) != 1 {
		t.Fatal("daily CMD blend missing")
	}
	if count(t, conn, "SELECT count(*) FROM blended_weekly_rates WHERE bucket_date = ? AND quote = 'CMD'", week) != 1 {
		t.Fatal("weekly CMD blend missing")
	}
	if count(t, conn, "SELECT count(*) FROM blended_monthly_rates WHERE bucket_date = ? AND quote = 'CMD'", month) != 1 {
		t.Fatal("monthly CMD blend missing")
	}
}

// sdrCase is one of the four SDR normalization specs
// (spec/{bota,rba,bnm,rbm}_sdr_migration_spec.rb), which differ only in
// provider, home currency, starting version and data.
type sdrCase struct {
	provider, home string
	from           int // the version before the provider's migration
	rows           []rate
	conflicting    []rate
	blended        float64   // the daily blend row "leaves ... alone" keeps
	orphan         [2]string // base, quote of the stale SDR rollup row
	merged         [2]string // base, quote of the merged XDR series
}

var sdrCases = []sdrCase{
	{
		provider: "BOTA", home: "TZS", from: 35,
		rows: []rate{
			{provider: "BOTA", date: "2026-01-01", base: "SDR", quote: "TZS", mid: f(3607.4158)},
			{provider: "BOTA", date: "2026-01-02", base: "USD", quote: "TZS", mid: f(2500.1234)},
			{provider: "BOTA", date: "2026-01-03", base: "SDR", quote: "TZS", mid: f(3608.1256)},
			{provider: "BOTA", date: "2026-01-03", base: "XDR", quote: "TZS", mid: f(3608.1256)},
			{provider: "BOTA", date: "2026-01-04", base: "XDR", quote: "TZS", mid: f(3609.2345)},
			{provider: "BOTA", date: "2026-01-05", base: "TZS", quote: "SDR", mid: f(0.0002771)},
			{provider: "BOTA", date: "2026-01-06", base: "SDR", quote: "TZS", bid: f(3600.1234), ask: f(3610.5678)},
		},
		conflicting: []rate{
			{provider: "BOTA", date: "2026-01-01", base: "SDR", quote: "TZS", mid: f(3600)},
			{provider: "BOTA", date: "2026-01-02", base: "SDR", quote: "TZS", mid: f(3610)},
			{provider: "BOTA", date: "2026-01-02", base: "XDR", quote: "TZS", bid: f(3600), ask: f(3620)},
		},
		blended: 2500, orphan: [2]string{"SDR", "TZS"}, merged: [2]string{"XDR", "TZS"},
	},
	{
		provider: "RBA", home: "AUD", from: 37,
		rows: []rate{
			{provider: "RBA", date: "2026-01-01", base: "AUD", quote: "SDR", mid: f(0.5131)},
			{provider: "RBA", date: "2026-01-02", base: "AUD", quote: "USD", mid: f(0.6828)},
			{provider: "RBA", date: "2026-01-03", base: "AUD", quote: "SDR", mid: f(0.5120)},
			{provider: "RBA", date: "2026-01-03", base: "AUD", quote: "XDR", mid: f(0.5120)},
			{provider: "RBA", date: "2026-01-04", base: "AUD", quote: "XDR", mid: f(0.5106)},
			{provider: "RBA", date: "2026-01-05", base: "SDR", quote: "AUD", mid: f(1.95)},
			{provider: "RBA", date: "2026-01-06", base: "AUD", quote: "SDR", bid: f(0.5100), ask: f(0.5150)},
		},
		conflicting: []rate{
			{provider: "RBA", date: "2026-01-01", base: "AUD", quote: "SDR", mid: f(0.51)},
			{provider: "RBA", date: "2026-01-02", base: "AUD", quote: "SDR", mid: f(0.52)},
			{provider: "RBA", date: "2026-01-02", base: "AUD", quote: "XDR", bid: f(0.51), ask: f(0.53)},
		},
		blended: 1.5, orphan: [2]string{"AUD", "SDR"}, merged: [2]string{"AUD", "XDR"},
	},
	{
		provider: "BNM", home: "MYR", from: 38,
		rows: []rate{
			{provider: "BNM", date: "2026-01-01", base: "SDR", quote: "MYR", mid: f(5.3538)},
			{provider: "BNM", date: "2026-01-02", base: "USD", quote: "MYR", mid: f(3.9300)},
			{provider: "BNM", date: "2026-01-03", base: "SDR", quote: "MYR", mid: f(5.3795)},
			{provider: "BNM", date: "2026-01-03", base: "XDR", quote: "MYR", mid: f(5.3795)},
			{provider: "BNM", date: "2026-01-04", base: "XDR", quote: "MYR", mid: f(5.3877)},
			{provider: "BNM", date: "2026-01-05", base: "MYR", quote: "SDR", mid: f(0.1858)},
			{provider: "BNM", date: "2026-01-06", base: "SDR", quote: "MYR", bid: f(5.3500), ask: f(5.3600)},
		},
		conflicting: []rate{
			{provider: "BNM", date: "2026-01-01", base: "SDR", quote: "MYR", mid: f(5.35)},
			{provider: "BNM", date: "2026-01-02", base: "SDR", quote: "MYR", mid: f(5.36)},
			{provider: "BNM", date: "2026-01-02", base: "XDR", quote: "MYR", bid: f(5.35), ask: f(5.37)},
		},
		blended: 4.4, orphan: [2]string{"SDR", "MYR"}, merged: [2]string{"XDR", "MYR"},
	},
	{
		provider: "RBM", home: "MWK", from: 39,
		rows: []rate{
			{provider: "RBM", date: "2026-01-01", base: "SDR", quote: "MWK", mid: f(2200.5)},
			{provider: "RBM", date: "2026-01-02", base: "USD", quote: "MWK", mid: f(1700.0)},
			{provider: "RBM", date: "2026-01-03", base: "SDR", quote: "MWK", mid: f(2210.0)},
			{provider: "RBM", date: "2026-01-03", base: "XDR", quote: "MWK", mid: f(2210.0)},
			{provider: "RBM", date: "2026-01-04", base: "XDR", quote: "MWK", mid: f(2215.0)},
			{provider: "RBM", date: "2026-01-05", base: "MWK", quote: "SDR", mid: f(0.00045)},
			{provider: "RBM", date: "2026-01-06", base: "SDR", quote: "MWK", bid: f(2200.0), ask: f(2220.0)},
		},
		conflicting: []rate{
			{provider: "RBM", date: "2026-01-01", base: "SDR", quote: "MWK", mid: f(2200)},
			{provider: "RBM", date: "2026-01-02", base: "SDR", quote: "MWK", mid: f(2210)},
			{provider: "RBM", date: "2026-01-02", base: "XDR", quote: "MWK", bid: f(2200), ask: f(2220)},
		},
		blended: 1700, orphan: [2]string{"SDR", "MWK"}, merged: [2]string{"XDR", "MWK"},
	},
}

// sdrDatabase is run_migration_script's setup: a database migrated to c.from
// with providers seeded.
func sdrDatabase(t *testing.T, c sdrCase) *sql.DB {
	t.Helper()
	conn, _ := empty(t)
	migrateTo(t, conn, c.from)
	if err := rates.SeedProviders(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	return conn
}

func xdr(code string) string {
	if code == "SDR" {
		return "XDR"
	}
	return code
}

// "completes setup with an unavailable cache and preserves repaired history
// through recovery"
func TestSDRMigrationCompletesSetupAndPreservesRepairedHistory(t *testing.T) {
	for _, c := range sdrCases {
		t.Run(c.provider, func(t *testing.T) {
			ctx := context.Background()
			today := rates.Today()
			conn := sdrDatabase(t, c)
			p := db.Lit(c.provider)

			insertRates(t, conn, c.rows...)
			insertRates(t, conn,
				rate{provider: "ECB", date: "2026-01-01", base: "USD", quote: "XDR", mid: f(0.74)},
				rate{provider: "ECB", date: "2026-01-02", base: "USD", quote: "XDR", mid: f(0.75)},
				rate{provider: "ECB", date: "2026-01-10", base: "USD", quote: "XDR", mid: f(0.76)},
				rate{provider: "ECB", date: "2026-01-20", base: "USD", quote: "XDR", mid: f(0.77)},
				rate{provider: "BOC", date: "2026-01-01", base: "SDR", quote: "CAD", mid: f(1.8)},
			)
			var expected []storedRow
			for _, r := range allRates(t, conn) {
				if r.Provider == c.provider {
					r.Base, r.Quote = xdr(r.Base), xdr(r.Quote)
				}
				if !slices.Contains(expected, r) {
					expected = append(expected, r)
				}
			}
			slices.SortFunc(expected, func(a, b storedRow) int {
				return strings.Compare(a.Provider+a.Date+a.Base+a.Quote, b.Provider+b.Date+b.Base+b.Quote)
			})
			if err := rates.RefreshSummaries(ctx, conn, []string{"SDR", "XDR", c.home, "USD", "CAD"}, ""); err != nil {
				t.Fatal(err)
			}
			for _, t2 := range rates.Rollups {
				b := rates.BucketSQL(t2.Precision, "date")
				mustExec(t, conn, "INSERT INTO "+t2.Name+" (bucket_date, provider, base, quote, rate) SELECT "+b+
					", provider, base, quote, avg(rate) FROM rates GROUP BY "+b+", provider, base, quote")
				mustExec(t, conn, "INSERT INTO "+t2.Name+" (bucket_date, provider, base, quote, rate) VALUES "+
					"('2025-12-01', ?, ?, ?, 999)", c.provider, c.orphan[0], c.orphan[1])
			}
			rebuildBlends(t, conn, today)
			for _, r := range blend.Rollups {
				mustExec(t, conn, "INSERT INTO "+r.Table+" (bucket_date, quote, rate) VALUES ('2025-12-01', 'EUR', 999)")
			}
			unaffectedBucket := bucket(rates.Week, "2026-01-20")
			unaffected := blendRows(t, conn, "blended_weekly_rates", "bucket_date = "+db.Lit(unaffectedBucket))
			if len(unaffected) == 0 {
				t.Fatal("missing unaffected fixture")
			}

			setup(t, conn)

			if v := version(t, conn); v < c.from+1 {
				t.Fatalf("migration incomplete: version %d", v)
			}
			if got := allRates(t, conn); !reflect.DeepEqual(got, expected) {
				t.Fatalf("changed native observations:\ngot  %+v\nwant %+v", got, expected)
			}
			if slices.Contains(unknown(t, conn, c.provider), "SDR") {
				t.Fatal("still unknown SDR")
			}
			if !slices.Contains(unknown(t, conn, "BOC"), "SDR") {
				t.Fatal("lost other provider exclusion")
			}
			for _, code := range []string{"XDR", c.home} {
				var start, end string
				if err := conn.QueryRow("SELECT date(start_date), date(end_date) FROM currency_coverages WHERE "+
					"provider_key = ? AND iso_code = ?", c.provider, code).Scan(&start, &end); err != nil {
					t.Fatalf("coverage for %s: %v", code, err)
				}
				if start != "2026-01-01" || end != "2026-01-06" {
					t.Fatalf("wrong coverage for %s: %s..%s", code, start, end)
				}
				if count(t, conn, "SELECT count(*) FROM currencies WHERE iso_code = ?", code) != 1 {
					t.Fatalf("missing catalogue %s", code)
				}
			}
			if count(t, conn, "SELECT count(*) FROM currencies WHERE iso_code = 'SDR'") != 0 {
				t.Fatal("SDR entered catalogue")
			}
			if dailyReady(t, conn) {
				t.Fatal("daily blend stayed ready")
			}
			if count(t, conn, "SELECT count(*) FROM blended_rates") != 0 {
				t.Fatal("partial daily invalidation")
			}
			for _, r := range blend.Rollups {
				if groupedReady(t, conn, r) {
					t.Fatalf("%s stayed ready", r.Table)
				}
				if count(t, conn, "SELECT count(*) FROM "+r.Table+" WHERE bucket_date = '2025-12-01'") != 0 {
					t.Fatalf("%s: orphan bucket retained", r.Table)
				}
				_, ok, err := r.Read(ctx, conn, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 6, 0, 0, 0, 0, time.UTC), today)
				if err != nil {
					t.Fatal(err)
				}
				if ok {
					t.Fatalf("%s: served affected bucket", r.Table)
				}
				if count(t, conn, "SELECT count(*) FROM "+r.Source.Name+" WHERE provider = "+p+
					" AND (base = 'SDR' OR quote = 'SDR')") != 0 {
					t.Fatalf("%s: retained %s SDR rollup", r.Source.Name, c.provider)
				}
			}
			if got := blendRows(t, conn, "blended_weekly_rates", "bucket_date = "+db.Lit(unaffectedBucket)); !reflect.DeepEqual(got, unaffected) {
				t.Fatalf("changed unrelated bucket: %v, want %v", got, unaffected)
			}

			// Exercise the same recovery lifecycle as bin/schedule, then
			// compare it with a clean full rebuild.
			if err := blend.RebuildDaily(ctx, conn, today); err != nil {
				t.Fatal(err)
			}
			populate(t, conn, today)
			if !dailyReady(t, conn) {
				t.Fatal("blended_rates not ready")
			}
			recoveredDaily := blendRows(t, conn, "blended_rates", "")
			mustExec(t, conn, "DELETE FROM blended_rates")
			if err := blend.RebuildDaily(ctx, conn, today); err != nil {
				t.Fatal(err)
			}
			if got := blendRows(t, conn, "blended_rates", ""); !reflect.DeepEqual(got, recoveredDaily) {
				t.Fatal("rebuild mismatch for blended_rates")
			}
			for _, r := range blend.Rollups {
				if !groupedReady(t, conn, r) {
					t.Fatalf("%s not ready", r.Table)
				}
				recovered := blendRows(t, conn, r.Table, "")
				mustExec(t, conn, "DELETE FROM "+r.Table)
				if err := r.Rebuild(ctx, conn, today); err != nil {
					t.Fatal(err)
				}
				if got := blendRows(t, conn, r.Table, ""); !reflect.DeepEqual(got, recovered) {
					t.Fatalf("rebuild mismatch for %s", r.Table)
				}
			}

			// Rollups average the merged daily history rather than averaging
			// old SDR/XDR averages.
			var want, got float64
			if err := conn.QueryRow("SELECT avg(rate) FROM rates WHERE provider = ? AND base = ? AND quote = ?",
				c.provider, c.merged[0], c.merged[1]).Scan(&want); err != nil {
				t.Fatal(err)
			}
			if err := conn.QueryRow("SELECT rate FROM monthly_rates WHERE provider = ? AND base = ? AND quote = ?",
				c.provider, c.merged[0], c.merged[1]).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("wrong merged rollup: %v, want %v", got, want)
			}
		})
	}
}

// "rolls back conflicting native components even when their effective rates
// match"
func TestSDRMigrationRollsBackConflictingComponents(t *testing.T) {
	for _, c := range sdrCases {
		t.Run(c.provider, func(t *testing.T) {
			conn := sdrDatabase(t, c)
			insertRates(t, conn, c.conflicting...)
			mustExec(t, conn, "INSERT INTO blended_rates (date, quote, rate) VALUES ('2026-01-01', ?, ?)", c.home, c.blended)
			before := allRates(t, conn)

			err := Up(context.Background(), conn)
			if err == nil {
				t.Fatal("accepted conflicting components")
			}
			if !strings.Contains(err.Error(), "conflicting SDR/XDR components") {
				t.Fatal(err)
			}
			if got := allRates(t, conn); !reflect.DeepEqual(got, before) {
				t.Fatal("partial repair committed")
			}
			if v := version(t, conn); v != c.from {
				t.Fatalf("migration marked complete: version %d", v)
			}
			if count(t, conn, "SELECT count(*) FROM blended_rates") != 1 {
				t.Fatal("invalidated blends after failure")
			}
		})
	}
}

// "leaves databases without <provider> SDR history alone"
func TestSDRMigrationLeavesDatabasesWithoutHistoryAlone(t *testing.T) {
	for _, c := range sdrCases {
		t.Run(c.provider, func(t *testing.T) {
			conn := sdrDatabase(t, c)
			mustExec(t, conn, "INSERT INTO blended_rates (date, quote, rate) VALUES ('2026-01-01', ?, ?)", c.home, c.blended)
			if err := Up(context.Background(), conn); err != nil {
				t.Fatal(err)
			}
			if count(t, conn, "SELECT count(*) FROM blended_rates") != 1 {
				t.Fatal("unnecessary invalidation")
			}
		})
	}
}

// retiredDatabase is spec/retired_currency_labels_migration_spec.rb's
// run_migration_script setup: a database migrated to 40 with providers
// seeded.
func retiredDatabase(t *testing.T) *sql.DB {
	t.Helper()
	conn, _ := empty(t)
	migrateTo(t, conn, 40)
	if err := rates.SeedProviders(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	return conn
}

// spec/retired_currency_labels_migration_spec.rb
func TestRetiredLabelsMigrationRelabelsRescalesAndDropsStoredRowsAndRetiresLegacySeries(t *testing.T) {
	ctx := context.Background()
	today := rates.Today()
	conn := retiredDatabase(t)

	rows := []rate{
		{provider: "CBU", date: "2008-09-09", base: "SDR", quote: "UZS", mid: f(2048.52)},
		{provider: "CBU", date: "2008-09-09", base: "USD", quote: "UZS", mid: f(1326.0)},
		{provider: "CBU", date: "2008-09-16", base: "SDR", quote: "UZS", mid: f(2044.11)},
		{provider: "CBU", date: "2008-09-16", base: "XDR", quote: "UZS", mid: f(2044.11)},
		{provider: "CBU", date: "2008-09-16", base: "USD", quote: "UZS", mid: f(1326.38)},
		{provider: "BOTA", date: "1999-07-01", base: "MXM", quote: "TZS", mid: f(0.0602)},
		{provider: "BOTA", date: "1999-07-01", base: "USD", quote: "TZS", mid: f(740.0)},
		{provider: "NBP", date: "2002-02-26", base: "BYB", quote: "PLN", mid: f(0.002494)},
		{provider: "NBP", date: "2002-02-26", base: "USD", quote: "PLN", mid: f(4.189)},
		{provider: "NBP", date: "2002-12-24", base: "AFA", quote: "PLN", mid: f(0.000816)},
		{provider: "NBP", date: "2002-12-24", base: "USD", quote: "PLN", mid: f(3.8388)},
		{provider: "NBP", date: "2003-01-07", base: "AFA", quote: "PLN", mid: f(0.089056)},
		{provider: "NBP", date: "2003-01-07", base: "USD", quote: "PLN", mid: f(3.8582)},
		{provider: "NBP", date: "2003-10-28", base: "AON", quote: "PLN", mid: f(0.0503)},
		{provider: "NBP", date: "2003-10-28", base: "USD", quote: "PLN", mid: f(3.9745)},
		{provider: "BOI", date: "2000-01-03", base: "BEL", quote: "ILS", mid: f(1.0313)},
		{provider: "BOI", date: "2000-01-03", base: "ATS", quote: "ILS", mid: f(3.0234)},
		{provider: "BOI", date: "2000-01-03", base: "ESP", quote: "ILS", mid: f(2.5004)},
		{provider: "BOI", date: "2000-01-03", base: "ITL", quote: "ILS", mid: f(2.1486)},
		{provider: "BOI", date: "2000-01-03", base: "CBK_L", quote: "ILS", mid: f(4.387)},
		{provider: "BOI", date: "2000-01-03", base: "USD", quote: "ILS", mid: f(4.124)},
		{provider: "BDI", date: "1999-07-02", base: "EUR", quote: "BGL", mid: f(1955.83)},
		{provider: "BDI", date: "1999-07-02", base: "EUR", quote: "USD", mid: f(1.0315)},
		{provider: "BDI", date: "2001-06-01", base: "EUR", quote: "BGL", mid: f(1947.0)},
		{provider: "BDI", date: "2001-06-01", base: "EUR", quote: "BGN", mid: f(1.947)},
		{provider: "BDI", date: "2001-06-01", base: "EUR", quote: "USD", mid: f(0.85)},
		{provider: "ECB", date: "2026-01-20", base: "EUR", quote: "USD", mid: f(1.17)},
		{provider: "ECB", date: "2026-01-20", base: "EUR", quote: "GBP", mid: f(0.87)},
	}
	insertRates(t, conn, rows...)
	var codes []string
	for _, r := range rows {
		codes = append(codes, r.base, r.quote)
	}
	if err := rates.RefreshSummaries(ctx, conn, codes, ""); err != nil {
		t.Fatal(err)
	}
	// Before its defunct entry, BGL's coverage ran to BDI's last quote.
	mustExec(t, conn, "UPDATE currency_coverages SET end_date = '2001-06-01' WHERE provider_key = 'BDI' AND "+
		"iso_code = 'BGL'")
	mustExec(t, conn, "UPDATE currencies SET end_date = '2001-06-01' WHERE iso_code = 'BGL'")
	for _, t2 := range rates.Rollups {
		b := rates.BucketSQL(t2.Precision, "date")
		mustExec(t, conn, "INSERT INTO "+t2.Name+" (bucket_date, provider, base, quote, rate) SELECT "+b+
			", provider, base, quote, avg(rate) FROM rates GROUP BY "+b+", provider, base, quote")
	}
	rebuildBlends(t, conn, today)
	unaffectedBucket := bucket(rates.Week, "2026-01-20")
	unaffected := blendRows(t, conn, "blended_weekly_rates", "bucket_date = "+db.Lit(unaffectedBucket))
	if len(unaffected) == 0 {
		t.Fatal("missing unaffected fixture")
	}
	expiredBucket := bucket(rates.Week, "2001-06-01")
	if count(t, conn, "SELECT count(*) FROM blended_weekly_rates WHERE bucket_date = ?", expiredBucket) == 0 {
		t.Fatal("missing expired fixture")
	}

	setup(t, conn)
	if v := version(t, conn); v < 41 {
		t.Fatalf("migration incomplete: version %d", v)
	}

	type stored struct {
		provider, date, base string
		mid                  float64
	}
	result, err := conn.Query("SELECT provider, date(date), base, mid FROM rates WHERE provider NOT IN ('BDI', 'ECB') " +
		"ORDER BY provider, date, base")
	if err != nil {
		t.Fatal(err)
	}
	var got []stored
	for result.Next() {
		var s stored
		if err := result.Scan(&s.provider, &s.date, &s.base, &s.mid); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	result.Close()
	expected := []stored{
		{"BOI", "2000-01-03", "ATS", 0.30234},
		{"BOI", "2000-01-03", "BEF", 0.10313},
		{"BOI", "2000-01-03", "ESP", 0.025004},
		{"BOI", "2000-01-03", "ITL", 0.0021486},
		{"BOI", "2000-01-03", "USD", 4.124},
		{"BOTA", "1999-07-01", "MZM", 0.0602},
		{"BOTA", "1999-07-01", "USD", 740.0},
		{"CBU", "2008-09-09", "USD", 1326.0},
		{"CBU", "2008-09-09", "XDR", 2048.52},
		{"CBU", "2008-09-16", "USD", 1326.38},
		{"CBU", "2008-09-16", "XDR", 2044.11},
		{"NBP", "2002-02-26", "BYR", 0.002494},
		{"NBP", "2002-02-26", "USD", 4.189},
		{"NBP", "2002-12-24", "AFA", 0.000816},
		{"NBP", "2002-12-24", "USD", 3.8388},
		{"NBP", "2003-01-07", "AFN", 0.089056},
		{"NBP", "2003-01-07", "USD", 3.8582},
		{"NBP", "2003-10-28", "AOA", 0.0503},
		{"NBP", "2003-10-28", "USD", 3.9745},
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("wrong repaired rows:\ngot  %+v\nwant %+v", got, expected)
	}
	if count(t, conn, "SELECT count(*) FROM rates WHERE provider = 'BDI'") != 5 {
		t.Fatal("touched BDI rows")
	}

	for _, key := range []string{"CBU", "BOTA", "NBP", "BOI"} {
		if codes := unknown(t, conn, key); len(codes) != 0 {
			t.Fatalf("%s still reports %v", key, codes)
		}
	}
	for _, code := range []string{"SDR", "MXM", "AON", "BEL", "CBK_L"} {
		if count(t, conn, "SELECT count(*) FROM weekly_rates WHERE base = ?", code) != 0 {
			t.Fatalf("%s rollup retained", code)
		}
	}
	var ats float64
	if err := conn.QueryRow("SELECT rate FROM monthly_rates WHERE provider = 'BOI' AND base = 'ATS'").
		Scan(&ats); err != nil || ats != 0.30234 {
		t.Fatalf("stale ATS rollup: %v (%v)", ats, err)
	}
	var coverageEnd, catalogueEnd string
	if err := conn.QueryRow("SELECT date(end_date) FROM currency_coverages WHERE provider_key = 'BDI' AND " +
		"iso_code = 'BGL'").Scan(&coverageEnd); err != nil || coverageEnd != "1999-07-02" {
		t.Fatalf("BGL coverage past retirement: %s (%v)", coverageEnd, err)
	}
	if err := conn.QueryRow("SELECT date(end_date) FROM currencies WHERE iso_code = 'BGL'").
		Scan(&catalogueEnd); err != nil || catalogueEnd != "1999-07-02" {
		t.Fatalf("BGL catalogue past retirement: %s (%v)", catalogueEnd, err)
	}
	if count(t, conn, "SELECT count(*) FROM currency_coverages WHERE provider_key = 'NBP' AND iso_code = 'AFN'") != 1 {
		t.Fatal("AFN coverage missing")
	}

	if dailyReady(t, conn) {
		t.Fatal("daily blend stayed ready")
	}
	if count(t, conn, "SELECT count(*) FROM blended_rates") != 0 {
		t.Fatal("partial daily invalidation")
	}
	if got := blendRows(t, conn, "blended_weekly_rates", "bucket_date = "+db.Lit(unaffectedBucket)); !reflect.DeepEqual(got, unaffected) {
		t.Fatalf("changed unrelated bucket: %v, want %v", got, unaffected)
	}
	if count(t, conn, "SELECT count(*) FROM blended_weekly_rates WHERE bucket_date = ?", expiredBucket) != 0 {
		t.Fatal("kept expired BGL bucket")
	}
	for _, r := range blend.Rollups {
		if groupedReady(t, conn, r) {
			t.Fatalf("%s stayed ready", r.Table)
		}
	}

	// Exercise the same recovery lifecycle as bin/schedule, then compare it
	// with a clean full rebuild.
	if err := blend.RebuildDaily(ctx, conn, today); err != nil {
		t.Fatal(err)
	}
	populate(t, conn, today)
	if count(t, conn, "SELECT count(*) FROM blended_rates WHERE quote = 'BGL' AND date >= '1999-07-05'") != 0 {
		t.Fatal("expired BGL blended")
	}
	if count(t, conn, "SELECT count(*) FROM blended_rates WHERE quote = 'XDR' AND date = '2008-09-09'") == 0 {
		t.Fatal("relabelled XDR missing")
	}
	if !dailyReady(t, conn) {
		t.Fatal("blended_rates not ready")
	}
	recoveredDaily := blendRows(t, conn, "blended_rates", "")
	mustExec(t, conn, "DELETE FROM blended_rates")
	if err := blend.RebuildDaily(ctx, conn, today); err != nil {
		t.Fatal(err)
	}
	if got := blendRows(t, conn, "blended_rates", ""); !reflect.DeepEqual(got, recoveredDaily) {
		t.Fatal("rebuild mismatch for blended_rates")
	}
	for _, r := range blend.Rollups {
		if !groupedReady(t, conn, r) {
			t.Fatalf("%s not ready", r.Table)
		}
		recovered := blendRows(t, conn, r.Table, "")
		mustExec(t, conn, "DELETE FROM "+r.Table)
		if err := r.Rebuild(ctx, conn, today); err != nil {
			t.Fatal(err)
		}
		if got := blendRows(t, conn, r.Table, ""); !reflect.DeepEqual(got, recovered) {
			t.Fatalf("rebuild mismatch for %s", r.Table)
		}
	}
}

// "rolls back on conflicting duplicates"
func TestRetiredLabelsMigrationRollsBackOnConflictingDuplicates(t *testing.T) {
	conn := retiredDatabase(t)
	insertRates(t, conn,
		rate{provider: "CBU", date: "2008-09-16", base: "SDR", quote: "UZS", mid: f(2044.11)},
		rate{provider: "CBU", date: "2008-09-16", base: "XDR", quote: "UZS", mid: f(2044.12)},
	)
	mustExec(t, conn, "INSERT INTO blended_rates (date, quote, rate) VALUES ('2008-09-16', 'UZS', 1326.38)")
	before := allRates(t, conn)

	err := Up(context.Background(), conn)
	if err == nil {
		t.Fatal("accepted conflicting components")
	}
	if !strings.Contains(err.Error(), "conflicting SDR/XDR components") {
		t.Fatal(err)
	}
	if got := allRates(t, conn); !reflect.DeepEqual(got, before) {
		t.Fatal("partial repair committed")
	}
	if v := version(t, conn); v != 40 {
		t.Fatalf("migration marked complete: version %d", v)
	}
	if count(t, conn, "SELECT count(*) FROM blended_rates") != 1 {
		t.Fatal("invalidated blends after failure")
	}
}

// "leaves databases without affected history alone"
func TestRetiredLabelsMigrationLeavesDatabasesWithoutAffectedHistoryAlone(t *testing.T) {
	conn := retiredDatabase(t)
	insertRates(t, conn, rate{provider: "ECB", date: "2026-01-20", base: "EUR", quote: "USD", mid: f(1.17)})
	mustExec(t, conn, "INSERT INTO blended_rates (date, quote, rate) VALUES ('2026-01-20', 'EUR', 0.85)")
	if err := Up(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	if count(t, conn, "SELECT count(*) FROM blended_rates") != 1 {
		t.Fatal("unnecessary invalidation")
	}
}

// afghaniDatabase is spec/bdi_old_afghani_migration_spec.rb's
// run_migration_script setup: a database migrated to 41 with providers
// seeded.
func afghaniDatabase(t *testing.T) *sql.DB {
	t.Helper()
	conn, _ := empty(t)
	migrateTo(t, conn, 41)
	if err := rates.SeedProviders(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	return conn
}

// spec/bdi_old_afghani_migration_spec.rb
func TestOldAfghaniMigrationRelabelsBDIRowsBeforeSwitch(t *testing.T) {
	ctx := context.Background()
	today := rates.Today()
	conn := afghaniDatabase(t)

	insertRates(t, conn,
		rate{provider: "BDI", date: "2002-10-04", base: "EUR", quote: "AFN", mid: f(4685.87)},
		rate{provider: "BDI", date: "2002-10-04", base: "EUR", quote: "USD", mid: f(0.9865)},
		rate{provider: "BDI", date: "2004-03-31", base: "EUR", quote: "AFN", mid: f(5806.4)},
		rate{provider: "BDI", date: "2004-03-31", base: "EUR", quote: "AFA", mid: f(5806.4)},
		rate{provider: "BDI", date: "2004-03-31", base: "EUR", quote: "USD", mid: f(1.2224)},
		rate{provider: "BDI", date: "2004-04-01", base: "EUR", quote: "AFN", mid: f(58.52)},
		rate{provider: "BDI", date: "2004-04-01", base: "EUR", quote: "USD", mid: f(1.232)},
		rate{provider: "NBP", date: "2004-03-31", base: "AFN", quote: "PLN", mid: f(0.0789)},
		rate{provider: "NBP", date: "2004-03-31", base: "USD", quote: "PLN", mid: f(3.9077)},
		rate{provider: "ECB", date: "2026-01-20", base: "EUR", quote: "USD", mid: f(1.17)},
	)
	if err := rates.RefreshSummaries(ctx, conn, []string{"AFA", "AFN", "EUR", "USD", "PLN"}, ""); err != nil {
		t.Fatal(err)
	}
	for _, t2 := range rates.Rollups {
		b := rates.BucketSQL(t2.Precision, "date")
		mustExec(t, conn, "INSERT INTO "+t2.Name+" (bucket_date, provider, base, quote, rate) SELECT "+b+
			", provider, base, quote, avg(rate) FROM rates GROUP BY "+b+", provider, base, quote")
	}
	rebuildBlends(t, conn, today)
	if count(t, conn, "SELECT count(*) FROM blended_rates WHERE quote = 'AFN' AND date = '2002-10-04'") == 0 {
		t.Fatal("fixture blend missing old AFN")
	}
	affectedBucket := bucket(rates.Week, "2002-10-04")
	unaffectedBucket := bucket(rates.Week, "2026-01-20")
	unaffected := blendRows(t, conn, "blended_weekly_rates", "bucket_date = "+db.Lit(unaffectedBucket))
	if len(unaffected) == 0 {
		t.Fatal("missing unaffected fixture")
	}

	setup(t, conn)
	if v := version(t, conn); v < 42 {
		t.Fatalf("migration incomplete: version %d", v)
	}

	type pair struct{ date, quote string }
	pairs := func(query string) []pair {
		t.Helper()
		result, err := conn.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		defer result.Close()
		var out []pair
		for result.Next() {
			var p pair
			if err := result.Scan(&p.date, &p.quote); err != nil {
				t.Fatal(err)
			}
			out = append(out, p)
		}
		return out
	}
	type stored struct {
		date, quote string
		mid         float64
	}
	result, err := conn.Query("SELECT date(date), quote, mid FROM rates WHERE provider = 'BDI' AND quote != 'USD' " +
		"ORDER BY date")
	if err != nil {
		t.Fatal(err)
	}
	var got []stored
	for result.Next() {
		var s stored
		if err := result.Scan(&s.date, &s.quote, &s.mid); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	result.Close()
	want := []stored{{"2002-10-04", "AFA", 4685.87}, {"2004-03-31", "AFA", 5806.4}, {"2004-04-01", "AFN", 58.52}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wrong relabelled rows: %+v", got)
	}
	if count(t, conn, "SELECT count(*) FROM rates WHERE provider = 'NBP' AND base = 'AFN'") != 1 {
		t.Fatal("touched other providers")
	}

	weekly := pairs("SELECT date(bucket_date), quote FROM weekly_rates WHERE provider = 'BDI' AND quote != 'USD' " +
		"ORDER BY bucket_date, quote")
	straddle := bucket(rates.Week, "2004-03-31")
	if want := []pair{{affectedBucket, "AFA"}, {straddle, "AFA"}, {straddle, "AFN"}}; !reflect.DeepEqual(weekly, want) {
		t.Fatalf("wrong weekly rollups: %+v", weekly)
	}
	monthly := pairs("SELECT date(bucket_date), quote FROM monthly_rates WHERE provider = 'BDI' AND quote = 'AFN'")
	if want := []pair{{bucket(rates.Month, "2004-04-01"), "AFN"}}; !reflect.DeepEqual(monthly, want) {
		t.Fatalf("AFN monthly rollup retained: %+v", monthly)
	}

	type coverage struct{ code, provider, start, end string }
	result, err = conn.Query("SELECT iso_code, provider_key, date(start_date), date(end_date) FROM currency_coverages " +
		"WHERE iso_code IN ('AFA', 'AFN') ORDER BY iso_code, provider_key")
	if err != nil {
		t.Fatal(err)
	}
	var coverages []coverage
	for result.Next() {
		var c coverage
		if err := result.Scan(&c.code, &c.provider, &c.start, &c.end); err != nil {
			t.Fatal(err)
		}
		coverages = append(coverages, c)
	}
	result.Close()
	wantCoverages := []coverage{
		{"AFA", "BDI", "2002-10-04", "2002-10-04"},
		{"AFN", "BDI", "2004-04-01", "2004-04-01"},
		{"AFN", "NBP", "2004-03-31", "2004-03-31"},
	}
	if !reflect.DeepEqual(coverages, wantCoverages) {
		t.Fatalf("wrong coverage: %+v", coverages)
	}
	for code, start := range map[string]string{"AFN": "2004-03-31", "AFA": "2002-10-04"} {
		var got string
		if err := conn.QueryRow("SELECT date(start_date) FROM currencies WHERE iso_code = ?", code).
			Scan(&got); err != nil || got != start {
			t.Fatalf("%s catalogue start %s (%v), want %s", code, got, err, start)
		}
	}

	if count(t, conn, "SELECT count(*) FROM blended_rates") != 0 {
		t.Fatal("partial daily invalidation")
	}
	if count(t, conn, "SELECT count(*) FROM blended_weekly_rates WHERE bucket_date = ?", affectedBucket) != 0 {
		t.Fatal("kept affected bucket")
	}
	if got := blendRows(t, conn, "blended_weekly_rates", "bucket_date = "+db.Lit(unaffectedBucket)); !reflect.DeepEqual(got, unaffected) {
		t.Fatalf("changed unrelated bucket: %v, want %v", got, unaffected)
	}

	if err := blend.RebuildDaily(ctx, conn, today); err != nil {
		t.Fatal(err)
	}
	populate(t, conn, today)
	if count(t, conn, "SELECT count(*) FROM blended_rates WHERE quote = 'AFN' AND date < '2004-03-31'") != 0 {
		t.Fatal("old afghani blended as AFN")
	}
	if count(t, conn, "SELECT count(*) FROM blended_rates WHERE quote = 'AFA' AND date = '2002-10-04'") == 0 {
		t.Fatal("AFA missing from blend")
	}
	if count(t, conn, "SELECT count(*) FROM blended_rates WHERE quote = 'AFA' AND date >= '2002-10-07'") != 0 {
		t.Fatal("expired AFA blended")
	}
	if !dailyReady(t, conn) {
		t.Fatal("blended_rates not ready")
	}
	recoveredDaily := blendRows(t, conn, "blended_rates", "")
	mustExec(t, conn, "DELETE FROM blended_rates")
	if err := blend.RebuildDaily(ctx, conn, today); err != nil {
		t.Fatal(err)
	}
	if got := blendRows(t, conn, "blended_rates", ""); !reflect.DeepEqual(got, recoveredDaily) {
		t.Fatal("rebuild mismatch for blended_rates")
	}
	for _, r := range blend.Rollups {
		if !groupedReady(t, conn, r) {
			t.Fatalf("%s not ready", r.Table)
		}
		recovered := blendRows(t, conn, r.Table, "")
		mustExec(t, conn, "DELETE FROM "+r.Table)
		if err := r.Rebuild(ctx, conn, today); err != nil {
			t.Fatal(err)
		}
		if got := blendRows(t, conn, r.Table, ""); !reflect.DeepEqual(got, recovered) {
			t.Fatalf("rebuild mismatch for %s", r.Table)
		}
	}
}

// "rolls back on conflicting duplicates"
func TestOldAfghaniMigrationRollsBackOnConflictingDuplicates(t *testing.T) {
	conn := afghaniDatabase(t)
	insertRates(t, conn,
		rate{provider: "BDI", date: "2004-03-31", base: "EUR", quote: "AFN", mid: f(5806.4)},
		rate{provider: "BDI", date: "2004-03-31", base: "EUR", quote: "AFA", mid: f(5806.5)},
	)
	mustExec(t, conn, "INSERT INTO blended_rates (date, quote, rate) VALUES ('2004-03-31', 'AFN', 4750.0)")
	before := allRates(t, conn)

	err := Up(context.Background(), conn)
	if err == nil {
		t.Fatal("accepted conflicting components")
	}
	if !strings.Contains(err.Error(), "conflicting AFN/AFA components") {
		t.Fatal(err)
	}
	if got := allRates(t, conn); !reflect.DeepEqual(got, before) {
		t.Fatal("partial repair committed")
	}
	if v := version(t, conn); v != 41 {
		t.Fatalf("migration marked complete: version %d", v)
	}
	if count(t, conn, "SELECT count(*) FROM blended_rates") != 1 {
		t.Fatal("invalidated blends after failure")
	}
}

// "leaves databases without affected history alone"
func TestOldAfghaniMigrationLeavesDatabasesWithoutAffectedHistoryAlone(t *testing.T) {
	conn := afghaniDatabase(t)
	insertRates(t, conn, rate{provider: "BDI", date: "2004-04-01", base: "EUR", quote: "AFN", mid: f(58.52)})
	mustExec(t, conn, "INSERT INTO blended_rates (date, quote, rate) VALUES ('2004-04-01', 'AFN', 47.5)")
	if err := Up(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	if count(t, conn, "SELECT count(*) FROM blended_rates") != 1 {
		t.Fatal("unnecessary invalidation")
	}
	if count(t, conn, "SELECT count(*) FROM rates WHERE quote = 'AFN'") != 1 {
		t.Fatal("relabelled post-switch row")
	}
}

// successorDatabase is spec/successor_values_migration_spec.rb's
// run_migration_script setup: a database migrated to 42 with providers seeded.
func successorDatabase(t *testing.T) *sql.DB {
	t.Helper()
	conn, _ := empty(t)
	migrateTo(t, conn, 42)
	if err := rates.SeedProviders(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	return conn
}

// spec/successor_values_migration_spec.rb
func TestSuccessorValuesMigrationRelabelsRescalesAndDropsStoredRowsAndLeavesTheBlendsToARebuild(t *testing.T) {
	ctx := context.Background()
	today := rates.Today()
	conn := successorDatabase(t)

	rows := []rate{
		{provider: "CBG", date: "2021-08-30", base: "SLL", quote: "GMD", mid: f(0.01)},
		{provider: "CBG", date: "2022-07-12", base: "SLL", quote: "GMD", mid: f(4.11)},
		{provider: "CBU", date: "2004-12-28", base: "TRL", quote: "UZS", mid: f(0.00078)},
		{provider: "CBU", date: "2005-01-04", base: "TRL", quote: "UZS", mid: f(787.09)},
		{provider: "CBU", date: "2009-02-17", base: "TRL", quote: "UZS", mid: f(849.84)},
		{provider: "CBU", date: "2009-02-17", base: "TRY", quote: "UZS", mid: f(849.84)},
		{provider: "LB", date: "1999-12-31", base: "BYR", quote: "LTL", mid: f(0.000004444)},
		{provider: "LB", date: "2000-01-03", base: "BYR", quote: "LTL", mid: f(0.0044444)},
		{provider: "NBU", date: "1999-01-04", base: "RUR", quote: "UAH", mid: f(0.16596)},
		{provider: "NBU", date: "2004-03-31", base: "RUR", quote: "UAH", mid: f(0.18709)},
		{provider: "NBU", date: "2004-03-31", base: "RUB", quote: "UAH", mid: f(0.18709)},
		{provider: "NBU", date: "1999-08-02", base: "BGL", quote: "UAH", mid: f(2.2594804)},
		{provider: "NBU", date: "2000-01-03", base: "BGL", quote: "UAH", mid: f(0.2712867)},
		{provider: "NBU", date: "2005-01-05", base: "TRL", quote: "UAH", mid: f(0.00000376)},
		{provider: "NBU", date: "2005-06-27", base: "TRL", quote: "UAH", mid: f(0.03729741)},
		{provider: "NBU", date: "2005-06-27", base: "USD", quote: "UAH", mid: f(5.055)},
		{provider: "BNA", date: "2014-04-22", base: "MZM", quote: "AOA", mid: f(3.1)},
		{provider: "BNA", date: "2023-02-17", base: "STD", quote: "AOA", mid: f(0.02398)},
		{provider: "BNA", date: "2023-10-18", base: "VEF", quote: "AOA", mid: f(23.74128)},
		{provider: "BNA", date: "2026-02-05", base: "VEF", quote: "AOA", mid: f(2.48819)},
		{provider: "BNA", date: "2026-02-05", base: "VES", quote: "AOA", mid: f(2.487)},
		{provider: "BDI", date: "2008-07-31", base: "EUR", quote: "ZWD", mid: f(108471581765.0)},
		{provider: "BDI", date: "2008-08-01", base: "EUR", quote: "ZWD", mid: f(11.805092)},
		{provider: "BDI", date: "2009-02-03", base: "EUR", quote: "ZWD", mid: f(28.2678)},
		{provider: "NBP", date: "2009-02-04", base: "ZWR", quote: "PLN", mid: f(1.0e-08)},
		{provider: "NBP", date: "2009-02-25", base: "ZWR", quote: "PLN", mid: f(0.043749)},
		{provider: "BAM", date: "2018-01-02", base: "MRO", quote: "MAD", mid: f(0.02624)},
		{provider: "BAM", date: "2018-04-16", base: "MRO", quote: "MAD", mid: f(0.25864)},
		{provider: "BAM", date: "2018-04-17", base: "MRO", quote: "MAD", mid: f(25.857)},
		{provider: "BAM", date: "2018-04-17", base: "USD", quote: "MAD", mid: f(9.1664)},
		{provider: "BOTA", date: "2025-06-21", base: "ZMK", quote: "TZS", mid: f(111.6619)},
		{provider: "NBKR", date: "2016-06-25", base: "BYR", quote: "KGS", mid: f(0.003402)},
		{provider: "NBKR", date: "2026-09-26", base: "BYR", quote: "KGS", mid: f(0.003402)},
		{provider: "ECB", date: "2026-01-20", base: "EUR", quote: "USD", mid: f(1.17)},
		{provider: "ECB", date: "2026-01-20", base: "EUR", quote: "GBP", mid: f(0.87)},
	}
	insertRates(t, conn, rows...)
	var codes []string
	for _, r := range rows {
		codes = append(codes, r.base, r.quote)
	}
	if err := rates.RefreshSummaries(ctx, conn, codes, ""); err != nil {
		t.Fatal(err)
	}
	for _, t2 := range rates.Rollups {
		b := rates.BucketSQL(t2.Precision, "date")
		mustExec(t, conn, "INSERT INTO "+t2.Name+" (bucket_date, provider, base, quote, rate) SELECT "+b+
			", provider, base, quote, avg(rate) FROM rates GROUP BY "+b+", provider, base, quote")
	}
	rebuildBlends(t, conn, today)
	if count(t, conn, "SELECT count(*) FROM blended_rates WHERE quote = 'TRL' AND date = '2005-06-27'") == 0 {
		t.Fatal("fixture blend missing TRL")
	}
	blendTables := []string{"blended_rates", "blended_weekly_rates", "blended_monthly_rates"}
	snapshot := func() map[string][]blendRow {
		out := map[string][]blendRow{}
		for _, table := range blendTables {
			out[table] = blendRows(t, conn, table, "")
		}
		return out
	}
	before := snapshot()

	setup(t, conn)
	if v := version(t, conn); v < 43 {
		t.Fatalf("migration incomplete: version %d", v)
	}

	type stored struct {
		provider, date, base, quote string
		mid                         float64
	}
	result, err := conn.Query("SELECT provider, date(date), base, quote, mid FROM rates WHERE provider != 'ECB' AND " +
		"base != 'USD' ORDER BY provider, date, base, quote")
	if err != nil {
		t.Fatal(err)
	}
	var got []stored
	for result.Next() {
		var s stored
		if err := result.Scan(&s.provider, &s.date, &s.base, &s.quote, &s.mid); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	result.Close()
	want := []stored{
		{"BAM", "2018-01-02", "MRO", "MAD", 0.02624},
		{"BAM", "2018-04-16", "MRU", "MAD", 0.25864},
		{"BAM", "2018-04-17", "MRU", "MAD", 0.25857},
		{"BDI", "2008-07-31", "EUR", "ZWD", 108471581765.0},
		{"BDI", "2008-08-01", "EUR", "ZWR", 11.805092},
		{"BDI", "2009-02-03", "EUR", "ZWL", 28.2678},
		{"BNA", "2014-04-22", "MZN", "AOA", 3.1},
		{"BNA", "2023-02-17", "STD", "AOA", 0.02398},
		{"BNA", "2023-10-18", "VES", "AOA", 23.74128},
		{"BNA", "2026-02-05", "VES", "AOA", 2.487},
		{"BOTA", "2025-06-21", "ZMW", "TZS", 111.6619},
		{"CBG", "2021-08-30", "SLL", "GMD", 0.01},
		{"CBG", "2022-07-12", "SLE", "GMD", 4.11},
		{"CBU", "2004-12-28", "TRL", "UZS", 0.00078},
		{"CBU", "2005-01-04", "TRY", "UZS", 787.09},
		{"CBU", "2009-02-17", "TRY", "UZS", 849.84},
		{"LB", "1999-12-31", "BYB", "LTL", 0.000004444},
		{"LB", "2000-01-03", "BYR", "LTL", 0.0044444},
		{"NBKR", "2016-06-25", "BYR", "KGS", 0.003402},
		{"NBP", "2009-02-04", "ZWR", "PLN", 1.0e-08},
		{"NBP", "2009-02-25", "ZWL", "PLN", 0.043749},
		{"NBU", "1999-01-04", "RUB", "UAH", 0.16596},
		{"NBU", "1999-08-02", "BGN", "UAH", 2.2594804},
		{"NBU", "2000-01-03", "BGN", "UAH", 2.712867},
		{"NBU", "2004-03-31", "RUB", "UAH", 0.18709},
		{"NBU", "2005-01-05", "TRL", "UAH", 0.00000376},
		{"NBU", "2005-06-27", "TRY", "UAH", 3.729741},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wrong repaired rows: %+v", got)
	}

	type rollup struct {
		base string
		rate float64
	}
	result, err = conn.Query("SELECT base, rate FROM weekly_rates WHERE provider = 'NBU' AND bucket_date = ? AND "+
		"base != 'USD'", bucket(rates.Week, "2005-06-27"))
	if err != nil {
		t.Fatal(err)
	}
	var nbu []rollup
	for result.Next() {
		var r rollup
		if err := result.Scan(&r.base, &r.rate); err != nil {
			t.Fatal(err)
		}
		nbu = append(nbu, r)
	}
	result.Close()
	if want := []rollup{{"TRY", 3.729741}}; !reflect.DeepEqual(nbu, want) {
		t.Fatalf("stale NBU rollup: %+v", nbu)
	}
	months, err := column(ctx, conn, "SELECT date(bucket_date) FROM monthly_rates WHERE provider = 'BAM' AND base = 'MRO'")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{bucket(rates.Month, "2018-01-02")}; !reflect.DeepEqual(months, want) {
		t.Fatalf("BAM rollup kept MRO: %v", months)
	}
	if count(t, conn, "SELECT count(*) FROM weekly_rates WHERE provider = 'NBKR'") != 1 {
		t.Fatal("NBKR rollup kept 2026 BYR")
	}

	coverage := func(provider, code string) [][2]string {
		t.Helper()
		result, err := conn.Query("SELECT date(start_date), date(end_date) FROM currency_coverages WHERE "+
			"provider_key = ? AND iso_code = ?", provider, code)
		if err != nil {
			t.Fatal(err)
		}
		defer result.Close()
		var out [][2]string
		for result.Next() {
			var c [2]string
			if err := result.Scan(&c[0], &c[1]); err != nil {
				t.Fatal(err)
			}
			out = append(out, c)
		}
		return out
	}
	for _, c := range []struct {
		provider, code string
		want           [][2]string
	}{
		{"NBU", "TRL", [][2]string{{"2005-01-05", "2005-01-05"}}},
		{"NBU", "RUR", nil},
		{"CBG", "SLE", [][2]string{{"2022-07-12", "2022-07-12"}}},
		{"LB", "BYB", [][2]string{{"1999-12-31", "1999-12-31"}}},
	} {
		if got := coverage(c.provider, c.code); !reflect.DeepEqual(got, c.want) {
			t.Fatalf("%s %s coverage %v, want %v", c.provider, c.code, got, c.want)
		}
	}
	var mruStart string
	if err := conn.QueryRow("SELECT date(start_date) FROM currencies WHERE iso_code = 'MRU'").
		Scan(&mruStart); err != nil || mruStart != "2018-04-16" {
		t.Fatalf("MRU catalogue start %s (%v)", mruStart, err)
	}

	if got := snapshot(); !reflect.DeepEqual(got, before) {
		t.Fatal("touched the blends")
	}

	rebuildBlends(t, conn, today)
	if count(t, conn, "SELECT count(*) FROM blended_rates WHERE quote = 'TRL' AND date = '2005-06-27'") != 0 {
		t.Fatal("TRL still blended in 2005")
	}
	blended := func(quote, date string) float64 {
		t.Helper()
		var v float64
		if err := conn.QueryRow("SELECT rate FROM blended_rates WHERE quote = ? AND date = ?", quote, date).
			Scan(&v); err != nil {
			t.Fatalf("%s blend on %s: %v", quote, date, err)
		}
		return v
	}
	if got, want := blended("TRY", "2005-06-27"), 5.055/3.729741; math.Abs(got-want) >= 1e-9 {
		t.Fatalf("TRY blend %v, want %v", got, want)
	}
	if got, want := blended("MRU", "2018-04-17"), 9.1664/0.25857; math.Abs(got-want) >= 1e-9 {
		t.Fatalf("MRU blend %v, want %v", got, want)
	}
}

// "rolls back on conflicting duplicates"
func TestSuccessorValuesMigrationRollsBackOnConflictingDuplicates(t *testing.T) {
	conn := successorDatabase(t)
	insertRates(t, conn,
		rate{provider: "CBU", date: "2009-02-17", base: "TRL", quote: "UZS", mid: f(849.84)},
		rate{provider: "CBU", date: "2009-02-17", base: "TRY", quote: "UZS", mid: f(849.85)},
	)
	mustExec(t, conn, "INSERT INTO blended_rates (date, quote, rate) VALUES ('2009-02-17', 'UZS', 1390.0)")
	before := allRates(t, conn)

	err := Up(context.Background(), conn)
	if err == nil {
		t.Fatal("accepted conflicting components")
	}
	if !strings.Contains(err.Error(), "conflicting TRL/TRY components") {
		t.Fatal(err)
	}
	if got := allRates(t, conn); !reflect.DeepEqual(got, before) {
		t.Fatal("partial repair committed")
	}
	if v := version(t, conn); v != 42 {
		t.Fatalf("migration marked complete: version %d", v)
	}
	if count(t, conn, "SELECT count(*) FROM blended_rates") != 1 {
		t.Fatal("touched the blends")
	}
}

// "leaves databases without affected history alone"
func TestSuccessorValuesMigrationLeavesDatabasesWithoutAffectedHistoryAlone(t *testing.T) {
	conn := successorDatabase(t)
	insertRates(t, conn,
		rate{provider: "CBU", date: "2004-12-28", base: "TRL", quote: "UZS", mid: f(0.00078)},
		rate{provider: "NBU", date: "2014-04-04", base: "TRY", quote: "UAH", mid: f(5.416837)},
	)
	mustExec(t, conn, "INSERT INTO blended_rates (date, quote, rate) VALUES ('2014-04-04', 'TRY', 2.14)")
	before := allRates(t, conn)
	if err := Up(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	if v := version(t, conn); v < 43 {
		t.Fatalf("migration incomplete: version %d", v)
	}
	if got := allRates(t, conn); !reflect.DeepEqual(got, before) {
		t.Fatal("changed unaffected rows")
	}
	if count(t, conn, "SELECT count(*) FROM blended_rates") != 1 {
		t.Fatal("touched the blends")
	}
}

// migratedDatabase is the run_migration_script setup of the NBRM ECU and rate
// spikes migration specs: a database migrated to version with providers
// seeded.
func migratedDatabase(t *testing.T, version int) *sql.DB {
	t.Helper()
	conn, _ := empty(t)
	migrateTo(t, conn, version)
	if err := rates.SeedProviders(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	return conn
}

// rollUp fills both provider rollup tables from every stored rate, as the
// migration specs do by hand.
func rollUp(t *testing.T, conn *sql.DB) {
	t.Helper()
	for _, t2 := range rates.Rollups {
		b := rates.BucketSQL(t2.Precision, "date")
		mustExec(t, conn, "INSERT INTO "+t2.Name+" (bucket_date, provider, base, quote, rate) SELECT "+b+
			", provider, base, quote, avg(rate) FROM rates GROUP BY "+b+", provider, base, quote")
	}
}

// blendSnapshot reads every blended table.
func blendSnapshot(t *testing.T, conn *sql.DB) map[string][]blendRow {
	t.Helper()
	out := map[string][]blendRow{}
	for _, table := range []string{"blended_rates", "blended_weekly_rates", "blended_monthly_rates"} {
		out[table] = blendRows(t, conn, table, "")
	}
	return out
}

// spec/nbrm_ecu_migration_spec.rb
func TestNBRMECUMigrationRelabelsTheECUCollapsesItsEuroDuplicatesAndLeavesTheBlendsToARebuild(t *testing.T) {
	ctx := context.Background()
	today := rates.Today()
	conn := migratedDatabase(t, 43)

	rows := []rate{
		{provider: "NBRM", date: "1996-06-03", base: "XBA", quote: "MKD", mid: f(50.2099)},
		{provider: "NBRM", date: "1996-06-03", base: "USD", quote: "MKD", mid: f(40.781)},
		{provider: "NBRM", date: "1998-12-31", base: "XBA", quote: "MKD", mid: f(60.9144)},
		{provider: "NBRM", date: "1999-01-04", base: "XBA", quote: "MKD", mid: f(60.5994)},
		{provider: "NBRM", date: "1999-01-04", base: "EUR", quote: "MKD", mid: f(60.5994)},
		{provider: "NBRM", date: "1999-05-04", base: "XBA", quote: "MKD", mid: f(60.6199)},
		{provider: "NBRM", date: "1999-05-04", base: "EUR", quote: "MKD", mid: f(60.6199)},
		{provider: "CNB", date: "1996-06-03", base: "XEU", quote: "CZK", mid: f(34.316)},
		{provider: "CNB", date: "1996-06-03", base: "USD", quote: "CZK", mid: f(27.86)},
	}
	insertRates(t, conn, rows...)
	var codes []string
	for _, r := range rows {
		codes = append(codes, r.base, r.quote)
	}
	if err := rates.RefreshSummaries(ctx, conn, codes, ""); err != nil {
		t.Fatal(err)
	}
	rollUp(t, conn)
	rebuildBlends(t, conn, today)
	if count(t, conn, "SELECT count(*) FROM blended_rates WHERE quote = 'XBA' AND date = '1996-06-03'") == 0 {
		t.Fatal("fixture blend missing XBA")
	}
	if count(t, conn, "SELECT count(*) FROM currencies WHERE iso_code = 'XBA'") == 0 {
		t.Fatal("fixture catalogue missing XBA")
	}
	before := blendSnapshot(t, conn)

	setup(t, conn)
	if v := version(t, conn); v < 44 {
		t.Fatalf("migration incomplete: version %d", v)
	}

	type stored struct {
		provider, date, base, quote string
		mid                         float64
	}
	result, err := conn.Query("SELECT provider, date(date), base, quote, mid FROM rates WHERE base != 'USD' " +
		"ORDER BY provider, date, base, quote")
	if err != nil {
		t.Fatal(err)
	}
	var got []stored
	for result.Next() {
		var s stored
		if err := result.Scan(&s.provider, &s.date, &s.base, &s.quote, &s.mid); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	result.Close()
	want := []stored{
		{"CNB", "1996-06-03", "XEU", "CZK", 34.316},
		{"NBRM", "1996-06-03", "XEU", "MKD", 50.2099},
		{"NBRM", "1998-12-31", "XEU", "MKD", 60.9144},
		{"NBRM", "1999-01-04", "EUR", "MKD", 60.5994},
		{"NBRM", "1999-05-04", "EUR", "MKD", 60.6199},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wrong repaired rows: %+v", got)
	}

	if count(t, conn, "SELECT count(*) FROM weekly_rates WHERE base = 'XBA'")+
		count(t, conn, "SELECT count(*) FROM monthly_rates WHERE base = 'XBA'") != 0 {
		t.Fatal("rollup kept XBA")
	}
	var eur []float64
	result, err = conn.Query("SELECT rate FROM weekly_rates WHERE provider = 'NBRM' AND bucket_date = ? AND "+
		"base = 'EUR'", bucket(rates.Week, "1999-05-04"))
	if err != nil {
		t.Fatal(err)
	}
	for result.Next() {
		var v float64
		if err := result.Scan(&v); err != nil {
			t.Fatal(err)
		}
		eur = append(eur, v)
	}
	result.Close()
	if want := []float64{60.6199}; !reflect.DeepEqual(eur, want) {
		t.Fatalf("stale EUR rollup: %v", eur)
	}

	coverage := func(code string) [][2]string {
		t.Helper()
		result, err := conn.Query("SELECT date(start_date), date(end_date) FROM currency_coverages WHERE "+
			"provider_key = 'NBRM' AND iso_code = ?", code)
		if err != nil {
			t.Fatal(err)
		}
		defer result.Close()
		var out [][2]string
		for result.Next() {
			var c [2]string
			if err := result.Scan(&c[0], &c[1]); err != nil {
				t.Fatal(err)
			}
			out = append(out, c)
		}
		return out
	}
	if got := coverage("XBA"); got != nil {
		t.Fatalf("XBA coverage kept: %v", got)
	}
	if got, want := coverage("XEU"), [][2]string{{"1996-06-03", "1998-12-31"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("XEU coverage %v", got)
	}
	if got, want := coverage("EUR"), [][2]string{{"1999-01-04", "1999-05-04"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("EUR coverage %v", got)
	}
	if count(t, conn, "SELECT count(*) FROM currencies WHERE iso_code = 'XBA'") != 0 {
		t.Fatal("XBA still catalogued")
	}

	if got := blendSnapshot(t, conn); !reflect.DeepEqual(got, before) {
		t.Fatal("touched the blends")
	}

	rebuildBlends(t, conn, today)
	if count(t, conn, "SELECT count(*) FROM blended_rates WHERE quote = 'XBA'") != 0 {
		t.Fatal("XBA still blended")
	}
	if count(t, conn, "SELECT count(*) FROM blended_rates WHERE quote = 'XEU' AND date = '1996-06-03'") == 0 {
		t.Fatal("XEU blend missing")
	}
}

// "rolls back on conflicting duplicates"
func TestNBRMECUMigrationRollsBackOnConflictingDuplicates(t *testing.T) {
	conn := migratedDatabase(t, 43)
	insertRates(t, conn,
		rate{provider: "NBRM", date: "1999-01-04", base: "XBA", quote: "MKD", mid: f(60.5994)},
		rate{provider: "NBRM", date: "1999-01-04", base: "EUR", quote: "MKD", mid: f(60.5995)},
	)
	mustExec(t, conn, "INSERT INTO blended_rates (date, quote, rate) VALUES ('1999-01-04', 'MKD', 36.0)")
	before := allRates(t, conn)

	err := Up(context.Background(), conn)
	if err == nil {
		t.Fatal("accepted conflicting components")
	}
	if !strings.Contains(err.Error(), "conflicting XBA/EUR components") {
		t.Fatal(err)
	}
	if got := allRates(t, conn); !reflect.DeepEqual(got, before) {
		t.Fatal("partial repair committed")
	}
	if v := version(t, conn); v != 43 {
		t.Fatalf("migration marked complete: version %d", v)
	}
	if count(t, conn, "SELECT count(*) FROM blended_rates") != 1 {
		t.Fatal("touched the blends")
	}
}

// "leaves databases without NBRM XBA history alone"
func TestNBRMECUMigrationLeavesDatabasesWithoutNBRMXBAHistoryAlone(t *testing.T) {
	conn := migratedDatabase(t, 43)
	insertRates(t, conn,
		rate{provider: "NBRM", date: "1999-01-04", base: "EUR", quote: "MKD", mid: f(60.5994)},
		rate{provider: "CBBH", date: "1998-01-06", base: "XEU", quote: "BAM", mid: f(1.97509972)},
	)
	mustExec(t, conn, "INSERT INTO blended_rates (date, quote, rate) VALUES ('1999-01-04', 'MKD', 36.0)")
	before := allRates(t, conn)
	if err := Up(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	if v := version(t, conn); v < 44 {
		t.Fatalf("migration incomplete: version %d", v)
	}
	if got := allRates(t, conn); !reflect.DeepEqual(got, before) {
		t.Fatal("changed unaffected rows")
	}
	if count(t, conn, "SELECT count(*) FROM blended_rates") != 1 {
		t.Fatal("touched the blends")
	}
}

// spec/rate_spikes_migration_spec.rb
func TestRateSpikesMigrationFlagsStoredOneDayTyposAndLeavesRatesAndBlendsToARebuild(t *testing.T) {
	today := rates.Today()
	conn := migratedDatabase(t, 44)

	var rows []rate
	for _, d := range []struct {
		date string
		mid  float64
	}{{"2024-01-18", 3.26}, {"2024-01-19", 3.26}, {"2024-01-22", 43.26}, {"2024-01-23", 3.26}} {
		rows = append(rows,
			rate{provider: "CBG", date: d.date, base: "SLE", quote: "GMD", mid: f(d.mid)},
			rate{provider: "CBG", date: d.date, base: "USD", quote: "GMD", mid: f(70.0)},
		)
	}
	// Lebanon's 2023 devaluation persists, so it stays in the blend.
	for _, d := range []struct {
		date string
		mid  float64
	}{{"2023-01-31", 1507.5}, {"2023-02-01", 15000.0}, {"2023-02-02", 15000.0}} {
		rows = append(rows, rate{provider: "BDL", date: d.date, base: "USD", quote: "LBP", mid: f(d.mid)})
	}
	insertRates(t, conn, rows...)
	rollUp(t, conn)
	rebuildBlends(t, conn, today)
	typo := func() (float64, bool) {
		t.Helper()
		var v float64
		err := conn.QueryRow("SELECT rate FROM blended_rates WHERE quote = 'SLE' AND date = '2024-01-22'").Scan(&v)
		if err == sql.ErrNoRows {
			return 0, false
		}
		if err != nil {
			t.Fatal(err)
		}
		return v, true
	}
	if v, ok := typo(); !ok || math.Abs(v-70.0/43.26) >= 1e-9 {
		t.Fatalf("fixture blend missing the typo: %v (%v)", v, ok)
	}
	before := blendSnapshot(t, conn)
	stored := allRates(t, conn)

	setup(t, conn)
	if v := version(t, conn); v < 45 {
		t.Fatalf("migration incomplete: version %d", v)
	}

	result, err := conn.Query("SELECT provider, date(date), base, quote FROM rate_spikes")
	if err != nil {
		t.Fatal(err)
	}
	var spikes [][4]string
	for result.Next() {
		var s [4]string
		if err := result.Scan(&s[0], &s[1], &s[2], &s[3]); err != nil {
			t.Fatal(err)
		}
		spikes = append(spikes, s)
	}
	result.Close()
	if want := [][4]string{{"CBG", "2024-01-22", "SLE", "GMD"}}; !reflect.DeepEqual(spikes, want) {
		t.Fatalf("wrong spikes: %v", spikes)
	}
	if got := allRates(t, conn); !reflect.DeepEqual(got, stored) {
		t.Fatal("changed rates")
	}
	if got := blendSnapshot(t, conn); !reflect.DeepEqual(got, before) {
		t.Fatal("touched the blends")
	}

	rebuildBlends(t, conn, today)
	if _, ok := typo(); ok {
		t.Fatal("typo still blended")
	}
	for _, r := range blendRows(t, conn, "blended_rates", "quote = 'SLE'") {
		if math.Abs(r.Rate-70.0/3.26) >= 1e-9 {
			t.Fatalf("SLE blend %v", r)
		}
	}
	var lbp float64
	if err := conn.QueryRow("SELECT rate FROM blended_rates WHERE quote = 'LBP' AND date = '2023-02-02'").
		Scan(&lbp); err != nil || lbp != 15000.0 {
		t.Fatalf("LBP blend %v (%v)", lbp, err)
	}
}

// spec/lb_and_cba_units_migration_spec.rb
func TestLBAndCBAUnitsMigrationRelabelsAndRescalesStoredRowsAndLeavesTheBlendsToARebuild(t *testing.T) {
	ctx := context.Background()
	conn := migratedDatabase(t, 45)

	rows := []rate{
		{provider: "LB", date: "1995-01-02", base: "PLN", quote: "LTL", mid: f(0.0001641)},
		{provider: "LB", date: "1995-01-03", base: "PLN", quote: "LTL", mid: f(1.646)},
		{provider: "LB", date: "1998-01-02", base: "RUB", quote: "LTL", mid: f(0.0006694)},
		{provider: "LB", date: "1998-01-05", base: "RUB", quote: "LTL", mid: f(0.6672)},
		{provider: "LB", date: "1999-07-06", base: "BGN", quote: "LTL", mid: f(0.0021467)},
		{provider: "LB", date: "1999-07-07", base: "BGN", quote: "LTL", mid: f(2.0952)},
		{provider: "LB", date: "2005-07-01", base: "RON", quote: "LTL", mid: f(0.000095538)},
		{provider: "LB", date: "2005-07-04", base: "RON", quote: "LTL", mid: f(0.95814)},
		{provider: "LB", date: "2006-07-07", base: "MZN", quote: "LTL", mid: f(0.00010536)},
		{provider: "LB", date: "2006-07-10", base: "MZN", quote: "LTL", mid: f(0.10531)},
		{provider: "CBA", date: "2000-10-30", base: "TJS", quote: "AMD", mid: f(2.671)},
		{provider: "CBA", date: "2000-11-01", base: "TJS", quote: "AMD", mid: f(250.74)},
		{provider: "CBA", date: "2004-12-30", base: "KZT", quote: "AMD", mid: f(37.37)},
		{provider: "CBA", date: "2005-01-04", base: "KZT", quote: "AMD", mid: f(3.739)},
		{provider: "NBU", date: "1997-12-31", base: "RUR", quote: "UAH", mid: f(0.000315)},
	}
	insertRates(t, conn, rows...)
	var codes []string
	for _, r := range rows {
		codes = append(codes, r.base, r.quote)
	}
	if err := rates.RefreshSummaries(ctx, conn, codes, ""); err != nil {
		t.Fatal(err)
	}
	rollUp(t, conn)
	mustExec(t, conn, "INSERT INTO blended_rates (date, quote, rate) VALUES ('1995-01-02', 'PLN', 24495.0)")
	mustExec(t, conn, "INSERT INTO rate_spikes (provider, date, base, quote) VALUES ('LB', '1995-01-02', 'PLN', 'LTL')")
	before := blendSnapshot(t, conn)

	setup(t, conn)
	if v := version(t, conn); v < 46 {
		t.Fatalf("migration incomplete: version %d", v)
	}

	type stored struct {
		provider, date, base string
		mid                  float64
	}
	result, err := conn.Query("SELECT provider, date(date), base, mid FROM rates ORDER BY provider, date, base")
	if err != nil {
		t.Fatal(err)
	}
	var got []stored
	for result.Next() {
		var s stored
		if err := result.Scan(&s.provider, &s.date, &s.base, &s.mid); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	result.Close()
	want := []stored{
		{"CBA", "2000-10-30", "TJR", 0.2671},
		{"CBA", "2000-11-01", "TJS", 250.74},
		{"CBA", "2004-12-30", "KZT", 3.737},
		{"CBA", "2005-01-04", "KZT", 3.739},
		{"LB", "1995-01-02", "PLZ", 0.0001641},
		{"LB", "1995-01-03", "PLN", 1.646},
		{"LB", "1998-01-02", "RUR", 0.0006694},
		{"LB", "1998-01-05", "RUB", 0.6672},
		{"LB", "1999-07-06", "BGL", 0.0021467},
		{"LB", "1999-07-07", "BGN", 2.0952},
		{"LB", "2005-07-01", "ROL", 0.000095538},
		{"LB", "2005-07-04", "RON", 0.95814},
		{"LB", "2006-07-07", "MZM", 0.00010536},
		{"LB", "2006-07-10", "MZN", 0.10531},
		{"NBU", "1997-12-31", "RUR", 0.000315},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wrong repaired rows: %+v", got)
	}

	rollup := func(table, where string, args ...any) []float64 {
		t.Helper()
		result, err := conn.Query("SELECT rate FROM "+table+" WHERE "+where, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer result.Close()
		var out []float64
		for result.Next() {
			var v float64
			if err := result.Scan(&v); err != nil {
				t.Fatal(err)
			}
			out = append(out, v)
		}
		return out
	}
	kzt := rollup("monthly_rates", "provider = 'CBA' AND base = 'KZT' AND bucket_date = ?", bucket(rates.Month,
		"2004-12-30"))
	if !reflect.DeepEqual(kzt, []float64{3.737}) {
		t.Fatalf("stale KZT rollup: %v", kzt)
	}
	if pln := rollup("weekly_rates", "provider = 'LB' AND base = 'PLN'"); !reflect.DeepEqual(pln, []float64{1.646}) {
		t.Fatalf("rollup kept PLN: %v", pln)
	}

	coverage := func(provider, code string) [][2]string {
		t.Helper()
		result, err := conn.Query("SELECT date(start_date), date(end_date) FROM currency_coverages WHERE "+
			"provider_key = ? AND iso_code = ?", provider, code)
		if err != nil {
			t.Fatal(err)
		}
		defer result.Close()
		var out [][2]string
		for result.Next() {
			var c [2]string
			if err := result.Scan(&c[0], &c[1]); err != nil {
				t.Fatal(err)
			}
			out = append(out, c)
		}
		return out
	}
	for _, c := range []struct {
		provider, code string
		want           [2]string
	}{
		{"LB", "PLN", [2]string{"1995-01-03", "1995-01-03"}},
		{"LB", "PLZ", [2]string{"1995-01-02", "1995-01-02"}},
		{"CBA", "TJR", [2]string{"2000-10-30", "2000-10-30"}},
		{"NBU", "RUR", [2]string{"1997-12-31", "1997-12-31"}},
	} {
		if got := coverage(c.provider, c.code); !reflect.DeepEqual(got, [][2]string{c.want}) {
			t.Fatalf("%s %s coverage %v, want %v", c.provider, c.code, got, c.want)
		}
	}

	if count(t, conn, "SELECT count(*) FROM rate_spikes WHERE provider = 'LB'") != 0 {
		t.Fatal("kept a stale spike flag")
	}
	if got := blendSnapshot(t, conn); !reflect.DeepEqual(got, before) {
		t.Fatal("touched the blends")
	}
}

// "rolls back on conflicting duplicates"
func TestLBAndCBAUnitsMigrationRollsBackOnConflictingDuplicates(t *testing.T) {
	conn := migratedDatabase(t, 45)
	insertRates(t, conn,
		rate{provider: "LB", date: "1995-01-02", base: "PLN", quote: "LTL", mid: f(0.0001641)},
		rate{provider: "LB", date: "1995-01-02", base: "PLZ", quote: "LTL", mid: f(0.0001642)},
	)
	mustExec(t, conn, "INSERT INTO blended_rates (date, quote, rate) VALUES ('1995-01-02', 'PLN', 24495.0)")
	before := allRates(t, conn)

	err := Up(context.Background(), conn)
	if err == nil {
		t.Fatal("accepted conflicting components")
	}
	if !strings.Contains(err.Error(), "conflicting PLN/PLZ components") {
		t.Fatal(err)
	}
	if got := allRates(t, conn); !reflect.DeepEqual(got, before) {
		t.Fatal("partial repair committed")
	}
	if v := version(t, conn); v != 45 {
		t.Fatalf("migration marked complete: version %d", v)
	}
	if count(t, conn, "SELECT count(*) FROM blended_rates") != 1 {
		t.Fatal("touched the blends")
	}
}

// "leaves databases without affected rows alone"
func TestLBAndCBAUnitsMigrationLeavesDatabasesWithoutAffectedRowsAlone(t *testing.T) {
	conn := migratedDatabase(t, 45)
	insertRates(t, conn,
		rate{provider: "LB", date: "1995-01-03", base: "PLN", quote: "LTL", mid: f(1.646)},
		rate{provider: "CBA", date: "2005-01-04", base: "KZT", quote: "AMD", mid: f(3.739)},
		rate{provider: "NBP", date: "1994-12-30", base: "USD", quote: "PLN", mid: f(2.4372)},
	)
	mustExec(t, conn, "INSERT INTO blended_rates (date, quote, rate) VALUES ('1995-01-03', 'PLN', 2.43)")
	before := allRates(t, conn)
	if err := Up(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	if v := version(t, conn); v < 46 {
		t.Fatalf("migration incomplete: version %d", v)
	}
	if got := allRates(t, conn); !reflect.DeepEqual(got, before) {
		t.Fatal("changed unaffected rows")
	}
	if count(t, conn, "SELECT count(*) FROM blended_rates") != 1 {
		t.Fatal("touched the blends")
	}
}
