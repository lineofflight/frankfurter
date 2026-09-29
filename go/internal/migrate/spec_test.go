package migrate

import (
	"context"
	"database/sql"
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

// The Ruby migration specs run each step in a child process against a scratch database; here each test gets its own
// database file. Ruby's db:setup is Up plus the provider seed. The specs also stub Cache.purge to fail, to show setup
// never needs the CDN; Go's migrations and seed have no cache dependency at all.

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
		// A standalone connection must read the schema without application callbacks.
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

// sdrCase is one of the four SDR normalization specs (spec/{bota,rba,bnm,rbm}_sdr_migration_spec.rb), which differ
// only in provider, home currency, starting version and data.
type sdrCase struct {
	provider, home string
	from           int // the version before the provider's migration
	rows           []rate
	conflicting    []rate
	blended        float64 // the daily blend row "leaves ... alone" keeps
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

// sdrDatabase is run_migration_script's setup: a database migrated to c.from with providers seeded.
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

// "completes setup with an unavailable cache and preserves repaired history through recovery"
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

			// Exercise the same recovery lifecycle as bin/schedule, then compare it with a clean full rebuild.
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

			// Rollups average the merged daily history rather than averaging old SDR/XDR averages.
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

// "rolls back conflicting native components even when their effective rates match"
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
