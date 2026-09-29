package blend

// Ports spec/blended_rollup_maintenance_spec.rb and spec/grouped_rollup_lock_spec.rb. The rake tasks are
// RebuildAll (blend:rebuild), RebuildProviderRollups (rollups:rebuild) and PurgeInvalid (db:purge_invalid); purging
// the cache is their caller's job.

import (
	"database/sql"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

func rebuildProvider(t *testing.T, conn *sql.DB, provider string) error {
	t.Helper()
	return RebuildProviderRollups(ctx, conn, provider, today())
}

func refreshProvider(t *testing.T, q db.Querier, provider string, dates ...time.Time) {
	t.Helper()
	if err := RefreshProviderRollups(ctx, q, provider, dates, today()); err != nil {
		t.Fatal(err)
	}
}

// purgeAt is RateValidation.purge with the future-date horizon at horizon, for every provider (the fixture providers
// declare no lead).
func purgeAt(t *testing.T, conn *sql.DB, horizon time.Time) (rates.PurgeTotals, error) {
	t.Helper()
	return rates.Purge(ctx, conn, horizon.AddDate(0, 0, -rates.MaxFutureDrift), map[string]int{})
}

func lastEUR(t *testing.T, q db.Querier, r Rollup) float64 {
	t.Helper()
	return queryFloat(t, q, "SELECT rate FROM "+r.Table+" WHERE quote = 'EUR' ORDER BY bucket_date DESC LIMIT 1")
}

func TestRebuildAllReadiesEveryMaterialization(t *testing.T) {
	conn := fixtures.New(t)
	if err := RebuildAll(ctx, conn, today()); err != nil {
		t.Fatal(err)
	}
	if !dailyReady(t, conn) {
		t.Error("daily not ready")
	}
	for _, r := range Rollups {
		if !ready(t, conn, r) {
			t.Errorf("%s not ready", r.Table)
		}
	}
}

func TestRebuildProviderRollupsRefreshesGroupedValues(t *testing.T) {
	conn := fixtures.New(t)
	for _, r := range Rollups {
		rebuild(t, conn, r)
	}
	exec(t, conn, "UPDATE rates SET mid = 1.3 WHERE provider = 'ECB' AND quote = 'USD'")
	if err := rebuildProvider(t, conn, "ecb"); err != nil {
		t.Fatal(err)
	}
	for _, r := range Rollups {
		stored := lastEUR(t, conn, r)
		rebuild(t, conn, r)
		if fresh := lastEUR(t, conn, r); fresh != stored {
			t.Errorf("%s: stored %v, rebuilt %v", r.Table, stored, fresh)
		}
	}
}

func TestPurgeInvalidatesGroupedValuesInSameTransaction(t *testing.T) {
	conn := fixtures.New(t)
	date := today().AddDate(0, 0, 500)
	insertRates(t, conn, []any{"ECB", day(date), "EUR", "USD", 1.2})
	refreshProvider(t, conn, "ECB", date)
	if n := queryInt(t, conn, "SELECT count(*) FROM blended_weekly_rates"); n == 0 {
		t.Fatal("no weekly blends")
	}
	for _, r := range Rollups {
		rebuild(t, conn, r)
	}
	before := queryInt(t, conn, "SELECT count(*) FROM blended_weekly_rates WHERE bucket_date < ?", day(today()))

	totals, err := rates.Purge(ctx, conn, today(), map[string]int{})
	if err != nil {
		t.Fatal(err)
	}
	if totals.Rates == 0 {
		t.Error("nothing purged")
	}
	if n := queryInt(t, conn, "SELECT count(*) FROM blended_weekly_rates WHERE bucket_date < ?", day(today())); n != before {
		t.Errorf("past weekly blends %d, want %d", n, before)
	}
	for _, r := range Rollups {
		if n := queryInt(t, conn, "SELECT count(*) FROM "+r.Table+" WHERE bucket_date > ?",
			day(today().AddDate(0, 0, 10))); n != 0 {
			t.Errorf("%s: %d future buckets", r.Table, n)
		}
	}
}

func TestPurgeInvalidRepopulatesGroupedTables(t *testing.T) {
	conn := fixtures.New(t)
	date := today().AddDate(0, 0, 500)
	insertRates(t, conn, []any{"ECB", day(date), "EUR", "USD", 1.2})
	refreshProvider(t, conn, "ECB", date)
	if _, err := PurgeInvalid(ctx, conn, today(), map[string]int{}); err != nil {
		t.Fatal(err)
	}
	for _, r := range Rollups {
		if !ready(t, conn, r) {
			t.Errorf("%s not ready", r.Table)
		}
	}
	if got := queryDate(t, conn, "SELECT max(bucket_date) FROM blended_weekly_rates"); got >= day(date) {
		t.Errorf("max bucket %s", got)
	}
}

func TestRebuildProviderRollupsReleasesSourceTransactionFirst(t *testing.T) {
	conn := fixtures.New(t)
	exec(t, conn, "UPDATE rates SET mid = 1.3 WHERE provider = 'ECB' AND quote = 'USD'")
	calls := 0
	withBatchHook(t, func(table string, _ []string) error {
		if table != Weekly.Table {
			return nil
		}
		calls++
		// Another connection sees only committed data: the rebuilt source rollups must already be there.
		c, err := conn.Conn(ctx)
		if err != nil {
			return err
		}
		defer c.Close()
		var rate float64
		if err := c.QueryRowContext(ctx, "SELECT rate FROM weekly_rates WHERE provider = 'ECB' AND quote = 'USD' "+
			"LIMIT 1").Scan(&rate); err != nil {
			return err
		}
		if rate != 1.3 {
			t.Errorf("source rollups not committed before recomputation: %v", rate)
		}
		return nil
	})
	if err := rebuildProvider(t, conn, "ecb"); err != nil {
		t.Fatal(err)
	}
	if calls == 0 {
		t.Error("weekly blends never refreshed")
	}
}

func TestRebuildProviderRollupsFailureLeavesChangedBucketsAbsent(t *testing.T) {
	conn := fixtures.New(t)
	date := "2000-01-01"
	insertSource(t, conn, Weekly, date, "BOC", "USD", "EUR", 0.8)
	for _, r := range Rollups {
		rebuild(t, conn, r)
	}
	prior := snapshot(t, conn, Weekly.Table, "bucket_date = ?", date)
	exec(t, conn, "UPDATE rates SET mid = 1.3 WHERE provider = 'ECB' AND quote = 'USD'")
	withBatchHook(t, func(table string, _ []string) error {
		if table == Weekly.Table {
			return errors.New("failed refresh")
		}
		return nil
	})
	if err := rebuildProvider(t, conn, "ecb"); err == nil {
		t.Fatal("no error")
	}
	if got := queryFloat(t, conn, "SELECT rate FROM weekly_rates WHERE provider = 'ECB' AND quote = 'USD'"); got != 1.3 {
		t.Errorf("weekly ECB USD = %v", got)
	}
	if got := snapshot(t, conn, Weekly.Table, "bucket_date = ?", date); !slices.Equal(got, prior) {
		t.Errorf("unrelated history changed: %v -> %v", prior, got)
	}
	if n := queryInt(t, conn, "SELECT count(*) FROM blended_weekly_rates WHERE bucket_date > ?", date); n != 0 {
		t.Errorf("changed buckets present: %d", n)
	}
}

func TestRebuildProviderRollupsOfExcludedProviderSkipsBlends(t *testing.T) {
	conn := fixtures.New(t)
	called := false
	withBatchHook(t, func(string, []string) error {
		called = true
		return nil
	})
	if err := rebuildProvider(t, conn, "ust"); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("grouped blends recomputed")
	}
}

func TestRebuildProviderRollupsRejectsUnknownProvider(t *testing.T) {
	conn := fixtures.New(t)
	if err := rebuildProvider(t, conn, "nope"); err == nil {
		t.Error("no error")
	}
}

func TestRebuildProviderRollupsRepairsRemovedAndReplacementBuckets(t *testing.T) {
	conn := fixtures.New(t)
	removed := "2000-01-01"
	kept := plus(removed, 40)
	for _, r := range Rollups {
		insertSource(t, conn, r, removed, "ECB", "USD", "EUR", 0.8)
		insertSource(t, conn, r, kept, "BOC", "USD", "EUR", 0.9)
		rebuild(t, conn, r)
	}
	date := adapter.Date(2001, 3, 12)
	insertRates(t, conn, []any{"ECB", day(date), "USD", "EUR", 0.7})
	if err := rebuildProvider(t, conn, "ecb"); err != nil {
		t.Fatal(err)
	}
	for _, r := range Rollups {
		if n := queryInt(t, conn, "SELECT count(*) FROM "+r.Table+" WHERE bucket_date = ?", removed); n != 0 {
			t.Errorf("%s: removed bucket has %d rows", r.Table, n)
		}
		if got := storedRate(t, conn, r, kept, "EUR"); got != 0.9 {
			t.Errorf("%s: kept EUR = %v", r.Table, got)
		}
		if got := storedRate(t, conn, r, bucketOf(t, conn, r.Source.Precision, date), "EUR"); got != 0.7 {
			t.Errorf("%s: replacement EUR = %v", r.Table, got)
		}
	}
}

func TestPurgeInvalidRepairsGroupedBucketsWhenDailyRebuildFails(t *testing.T) {
	conn := fixtures.New(t)
	date := today().AddDate(0, 0, 500)
	insertRates(t, conn, []any{"ECB", day(date), "USD", "XDR", 3.0})
	refreshProvider(t, conn, "ECB", date)
	for _, r := range Rollups {
		rebuild(t, conn, r)
	}
	withChunkHook(t, func(func() error) error { return errors.New("daily rebuild failed") })
	totals, err := PurgeInvalid(ctx, conn, today(), map[string]int{})
	if err == nil {
		t.Fatal("no error")
	}
	// Non-zero totals alongside the error tell the caller to purge the cache anyway.
	if totals.Total() == 0 {
		t.Error("totals lost")
	}
	for _, r := range Rollups {
		if !ready(t, conn, r) {
			t.Errorf("%s not ready", r.Table)
		}
	}
	if n := queryInt(t, conn, "SELECT count(*) FROM blended_weekly_rates WHERE quote = 'XDR'"); n != 0 {
		t.Errorf("XDR rows = %d", n)
	}
}

func xdrRollup(t *testing.T, conn *sql.DB, table, bucket string) float64 {
	t.Helper()
	return queryFloat(t, conn, "SELECT rate FROM "+table+" WHERE provider = 'ECB' AND bucket_date = ? AND quote = 'XDR'",
		bucket)
}

func TestPurgeRecomputesWeeklyAverageAcrossHorizon(t *testing.T) {
	conn := fixtures.New(t)
	dates := []time.Time{adapter.Date(2030, 2, 28), adapter.Date(2030, 3, 1)}
	insertRates(t, conn, []any{"ECB", day(dates[0]), "USD", "XDR", 15.0}, []any{"ECB", day(dates[1]), "USD", "XDR", 30.0})
	refreshProvider(t, conn, "ECB", dates...)
	bucket := bucketOf(t, conn, rates.Week, dates[1])
	if got := xdrRollup(t, conn, "weekly_rates", bucket); got != 22.5 {
		t.Fatalf("weekly = %v", got)
	}

	if _, err := purgeAt(t, conn, dates[0]); err != nil {
		t.Fatal(err)
	}
	if got := xdrRollup(t, conn, "weekly_rates", bucket); got != 15.0 {
		t.Errorf("weekly after purge = %v", got)
	}
	if n := queryInt(t, conn, "SELECT count(*) FROM blended_weekly_rates WHERE bucket_date = ?", bucket); n != 0 {
		t.Errorf("blends = %d", n)
	}
	populate(t, conn, Weekly)
	if got := storedRate(t, conn, Weekly, bucket, "XDR"); got != 15.0 {
		t.Errorf("repopulated XDR = %v", got)
	}
}

func TestPurgeRecomputesMonthlyAverageAcrossHorizon(t *testing.T) {
	conn := fixtures.New(t)
	dates := []time.Time{adapter.Date(2030, 8, 19), adapter.Date(2030, 8, 20)}
	insertRates(t, conn, []any{"ECB", day(dates[0]), "USD", "XDR", 100.0}, []any{"ECB", day(dates[1]), "USD", "XDR", 300.0})
	refreshProvider(t, conn, "ECB", dates...)
	bucket := bucketOf(t, conn, rates.Month, dates[1])
	if got := xdrRollup(t, conn, "monthly_rates", bucket); got != 200.0 {
		t.Fatalf("monthly = %v", got)
	}
	if _, err := purgeAt(t, conn, dates[0]); err != nil {
		t.Fatal(err)
	}
	if got := xdrRollup(t, conn, "monthly_rates", bucket); got != 100.0 {
		t.Errorf("monthly after purge = %v", got)
	}
	if n := queryInt(t, conn, "SELECT count(*) FROM blended_monthly_rates WHERE bucket_date = ?", bucket); n != 0 {
		t.Errorf("blends = %d", n)
	}
}

func TestPurgeRemovesStaleSourceBuckets(t *testing.T) {
	conn := fixtures.New(t)
	date := today().AddDate(0, 0, 500)
	insertRates(t, conn, []any{"ECB", day(date), "USD", "XDR", 30.0})
	refreshProvider(t, conn, "ECB", date)
	bucket := bucketOf(t, conn, rates.Week, date)
	if _, err := rates.Purge(ctx, conn, today(), map[string]int{}); err != nil {
		t.Fatal(err)
	}
	if n := queryInt(t, conn, "SELECT count(*) FROM weekly_rates WHERE provider = 'ECB' AND bucket_date = ?", bucket); n != 0 {
		t.Errorf("source rows = %d", n)
	}
	if n := queryInt(t, conn, "SELECT count(*) FROM blended_weekly_rates WHERE bucket_date = ?", bucket); n != 0 {
		t.Errorf("blends = %d", n)
	}
}

func TestPurgeKeepsRepairedBucketsStable(t *testing.T) {
	conn := fixtures.New(t)
	dates := []time.Time{adapter.Date(2030, 1, 1), adapter.Date(2030, 1, 4)}
	insertRates(t, conn, []any{"ECB", day(dates[0]), "USD", "EUR", 2.0}, []any{"ECB", day(dates[1]), "USD", "EUR", 100.0})
	refreshProvider(t, conn, "ECB", dates...)
	bucket := bucketOf(t, conn, rates.Month, dates[1])
	monthly := "SELECT rate FROM monthly_rates WHERE provider = 'ECB' AND bucket_date = ? AND quote = 'EUR'"

	if _, err := purgeAt(t, conn, dates[0]); err != nil {
		t.Fatal(err)
	}
	if n := queryInt(t, conn, "SELECT count(*) FROM rates WHERE provider = 'ECB' AND quote = 'EUR' AND date IN (?, ?)",
		day(dates[0]), day(dates[1])); n != 1 || queryDate(t, conn, "SELECT date FROM rates WHERE provider = 'ECB' AND "+
		"quote = 'EUR' AND date IN (?, ?)", day(dates[0]), day(dates[1])) != day(dates[0]) {
		t.Errorf("surviving dailies = %d", n)
	}
	if got := queryFloat(t, conn, monthly, bucket); got != 2.0 {
		t.Errorf("monthly = %v", got)
	}
	if _, err := purgeAt(t, conn, dates[0]); err != nil {
		t.Fatal(err)
	}
	if got := queryFloat(t, conn, monthly, bucket); got != 2.0 {
		t.Errorf("monthly after second purge = %v", got)
	}
}

func TestPurgeRepairsExcludedProviderWithoutInvalidatingBlend(t *testing.T) {
	conn := fixtures.New(t)
	dates := []time.Time{adapter.Date(2030, 2, 28), adapter.Date(2030, 3, 1)}
	insertRates(t, conn, []any{"ECB", day(dates[0]), "USD", "EUR", 0.8})
	refreshProvider(t, conn, "ECB", dates[0])
	insertRates(t, conn, []any{"UST", day(dates[0]), "USD", "XDR", 15.0}, []any{"UST", day(dates[1]), "USD", "XDR", 30.0})
	refreshProvider(t, conn, "UST", dates...)
	bucket := bucketOf(t, conn, rates.Week, dates[1])
	prior := snapshot(t, conn, Weekly.Table, "bucket_date = ?", bucket)

	if _, err := purgeAt(t, conn, dates[0]); err != nil {
		t.Fatal(err)
	}
	if got := queryFloat(t, conn, "SELECT rate FROM weekly_rates WHERE provider = 'UST' AND bucket_date = ? AND "+
		"quote = 'XDR'", bucket); got != 15.0 {
		t.Errorf("UST weekly = %v", got)
	}
	if got := snapshot(t, conn, Weekly.Table, "bucket_date = ?", bucket); !slices.Equal(got, prior) {
		t.Errorf("blend changed: %v -> %v", prior, got)
	}
}

func TestPurgeRollsBackWhenBucketRepairFails(t *testing.T) {
	conn := fixtures.New(t)
	dates := []time.Time{adapter.Date(2030, 2, 28), adapter.Date(2030, 3, 1)}
	insertRates(t, conn, []any{"ECB", day(dates[0]), "USD", "XDR", 15.0}, []any{"ECB", day(dates[1]), "USD", "XDR", 30.0})
	refreshProvider(t, conn, "ECB", dates...)
	bucket := bucketOf(t, conn, rates.Week, dates[1])
	prior := snapshot(t, conn, Weekly.Table, "bucket_date = ?", bucket)
	exec(t, conn, "CREATE TRIGGER fail_rollup_repair BEFORE INSERT ON weekly_rates BEGIN SELECT RAISE(ABORT, "+
		"'failed provider bucket repair'); END")
	if _, err := purgeAt(t, conn, dates[0]); err == nil {
		t.Fatal("no error")
	}
	exec(t, conn, "DROP TRIGGER fail_rollup_repair")

	if n := queryInt(t, conn, "SELECT count(*) FROM rates WHERE provider = 'ECB' AND quote = 'XDR'"); n != 2 {
		t.Errorf("dailies = %d", n)
	}
	if got := xdrRollup(t, conn, "weekly_rates", bucket); got != 22.5 {
		t.Errorf("weekly = %v", got)
	}
	if got := snapshot(t, conn, Weekly.Table, "bucket_date = ?", bucket); !slices.Equal(got, prior) {
		t.Errorf("blend changed: %v -> %v", prior, got)
	}
}

// grouped_rollup_lock_spec.rb: the source read runs under the writer lock, so a second connection's write is excluded
// until the source transaction commits; the refill runs after it has, so the same write then succeeds.
func TestRebuildProviderRollupsExcludesConcurrentWrites(t *testing.T) {
	t.Setenv("SQLITE_BUSY_TIMEOUT", "100")
	conn := fixtures.New(t)
	var path string
	if err := conn.QueryRowContext(ctx, "SELECT file FROM pragma_database_list WHERE name = 'main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	writer, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	insert := func() error {
		_, err := writer.ExecContext(ctx, "INSERT INTO rates (provider, date, base, quote, mid) VALUES "+
			"('ECB', '2024-01-02', 'USD', 'EUR', 0.8)")
		return err
	}
	insertRates(t, conn, []any{"ECB", "2024-01-01", "USD", "EUR", 0.8})

	attempted, blocked := false, false
	originalRead, originalRefill := afterSourceRead, beforeRefill
	t.Cleanup(func() { afterSourceRead, beforeRefill = originalRead, originalRefill })
	afterSourceRead = func() {
		attempted = true
		blocked = insert() != nil
	}
	refilled := false
	beforeRefill = func(table string) {
		if table != Weekly.Table {
			return
		}
		refilled = true
		if n := queryInt(t, writer, "SELECT count(*) FROM weekly_rates WHERE provider = 'ECB'"); n == 0 {
			t.Error("source rollups were not committed before recomputation")
		}
		if err := insert(); err != nil {
			t.Errorf("write after the source transaction: %v", err)
		}
	}
	if err := rebuildProvider(t, conn, "ECB"); err != nil {
		t.Fatal(err)
	}
	if !attempted || !blocked {
		t.Error("source read was not protected by the writer lock")
	}
	if !refilled {
		t.Error("no refill")
	}
	if n := queryInt(t, conn, "SELECT count(*) FROM blended_weekly_rates"); n == 0 {
		t.Error("missing grouped output")
	}
}
