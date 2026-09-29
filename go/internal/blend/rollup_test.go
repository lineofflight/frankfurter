package blend

// Ports spec/blended_rollup_spec.rb.

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

func TestGroupedBlendTablesExist(t *testing.T) {
	conn := fixtures.New(t)
	for _, r := range Rollups {
		queryInt(t, conn, "SELECT count(*) FROM "+r.Table)
	}
}

// withBatchHook swaps the beforeBatch seam for the test.
func withBatchHook(t *testing.T, hook func(table string, buckets []string) error) {
	original := beforeBatch
	beforeBatch = hook
	t.Cleanup(func() { beforeBatch = original })
}

func refresh(t *testing.T, q db.Querier, r Rollup, buckets ...string) int {
	t.Helper()
	n, err := r.Refresh(ctx, q, buckets, today())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func rebuild(t *testing.T, q db.Querier, r Rollup) {
	t.Helper()
	if err := r.Rebuild(ctx, q, today()); err != nil {
		t.Fatal(err)
	}
}

func populate(t *testing.T, q db.Querier, r Rollup) int {
	t.Helper()
	n, err := r.Populate(ctx, q, today())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func ready(t *testing.T, q db.Querier, r Rollup) bool {
	t.Helper()
	ok, err := r.Ready(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func storedRate(t *testing.T, q db.Querier, r Rollup, bucket, quote string) float64 {
	t.Helper()
	return queryFloat(t, q, "SELECT rate FROM "+r.Table+" WHERE bucket_date = ? AND quote = ?", bucket, quote)
}

// snapshot is model.dataset.order(:bucket_date, :quote).all.map(&:values), optionally filtered.
func snapshot(t *testing.T, q db.Querier, table, where string, args ...any) []string {
	t.Helper()
	if where == "" {
		where = "1"
	}
	rows, err := q.QueryContext(ctx, "SELECT * FROM "+table+" WHERE "+where+" ORDER BY 1, 2, 3", args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var out []string
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprint(values...))
	}
	return out
}

var jan1 = "2024-01-01"

func plus(date string, days int) string {
	d, _ := db.ParseDate(date)
	return day(d.AddDate(0, 0, days))
}

func insertSource(t *testing.T, q db.Querier, r Rollup, bucket, provider, base, quote string, rate float64) {
	t.Helper()
	exec(t, q, "INSERT INTO "+r.Source.Name+" (bucket_date, provider, base, quote, rate) VALUES (?, ?, ?, ?, ?)",
		bucket, provider, base, quote, rate)
}

// groupedSetup is the per-model before block: empty tables and three source rows on 2024-01-01.
func groupedSetup(t *testing.T, r Rollup) *sql.DB {
	conn := fixtures.New(t)
	exec(t, conn, "DELETE FROM "+r.Source.Name)
	exec(t, conn, "DELETE FROM "+r.Table)
	insertSource(t, conn, r, jan1, "ECB", "EUR", "USD", 2.0)
	insertSource(t, conn, r, jan1, "ECB", "EUR", "CHF", 1.0)
	insertSource(t, conn, r, jan1, "BOC", "USD", "EUR", 0.5)
	return conn
}

func eachRollup(t *testing.T, test func(t *testing.T, r Rollup)) {
	for _, r := range Rollups {
		t.Run(r.Table, func(t *testing.T) { test(t, r) })
	}
}

func TestRollupStoresFullPivotBlend(t *testing.T) {
	eachRollup(t, func(t *testing.T, r Rollup) {
		conn := groupedSetup(t, r)
		rebuild(t, conn, r)
		if got := storedRate(t, conn, r, jan1, "EUR"); got != 0.5 {
			t.Errorf("EUR = %v", got)
		}
		if got := storedRate(t, conn, r, jan1, "CHF"); got != 0.5 {
			t.Errorf("CHF = %v", got)
		}
		if !ready(t, conn, r) {
			t.Error("not ready")
		}
	})
}

func TestRollupRefreshesChangedBucketsOnly(t *testing.T) {
	eachRollup(t, func(t *testing.T, r Rollup) {
		conn := groupedSetup(t, r)
		other := plus(jan1, 40)
		insertSource(t, conn, r, other, "ECB", "USD", "EUR", 0.75)
		rebuild(t, conn, r)
		exec(t, conn, "UPDATE "+r.Source.Name+" SET rate = 1.5 WHERE bucket_date = ? AND quote = 'CHF'", jan1)
		refresh(t, conn, r, jan1)
		if got := storedRate(t, conn, r, jan1, "CHF"); got != 0.75 {
			t.Errorf("CHF = %v", got)
		}
		if got := storedRate(t, conn, r, other, "EUR"); got != 0.75 {
			t.Errorf("other EUR = %v", got)
		}
	})
}

func TestRollupRemovesBucketsWhoseSourceDisappeared(t *testing.T) {
	eachRollup(t, func(t *testing.T, r Rollup) {
		conn := groupedSetup(t, r)
		other := plus(jan1, 40)
		insertSource(t, conn, r, other, "ECB", "USD", "EUR", 0.75)
		rebuild(t, conn, r)
		exec(t, conn, "DELETE FROM "+r.Source.Name+" WHERE bucket_date = ?", jan1)
		rebuild(t, conn, r)
		if n := queryInt(t, conn, "SELECT count(*) FROM "+r.Table+" WHERE bucket_date = ?", jan1); n != 0 {
			t.Errorf("removed bucket rows = %d", n)
		}
		if n := queryInt(t, conn, "SELECT count(*) FROM "+r.Table+" WHERE bucket_date = ?", other); n == 0 {
			t.Error("kept bucket empty")
		}
		exec(t, conn, "DELETE FROM "+r.Source.Name)
		rebuild(t, conn, r)
		if n := queryInt(t, conn, "SELECT count(*) FROM "+r.Table); n != 0 {
			t.Errorf("rows = %d", n)
		}
	})
}

func read(t *testing.T, q db.Querier, r Rollup, start, end string) ([]string, bool) {
	t.Helper()
	s, _ := db.ParseDate(start)
	e, _ := db.ParseDate(end)
	rows, ok, err := r.Read(ctx, q, s, e, today())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, b := range rows {
		out = append(out, fmt.Sprint(day(b.Date), b.Base, b.Quote, b.Rate))
	}
	return out, ok
}

func TestRollupFallsBackWhenAnyBucketIsMissing(t *testing.T) {
	eachRollup(t, func(t *testing.T, r Rollup) {
		conn := groupedSetup(t, r)
		other := plus(jan1, 40)
		insertSource(t, conn, r, other, "ECB", "USD", "EUR", 0.75)
		refresh(t, conn, r, jan1)
		if ready(t, conn, r) {
			t.Error("ready")
		}
		if _, ok := read(t, conn, r, jan1, other); ok {
			t.Error("read a partially materialized range")
		}
		if rows, ok := read(t, conn, r, jan1, jan1); !ok || len(rows) == 0 {
			t.Errorf("read = %v, %v", rows, ok)
		}
	})
}

func TestRollupDoesNotSnapBackOverBucketWithoutBridge(t *testing.T) {
	eachRollup(t, func(t *testing.T, r Rollup) {
		conn := groupedSetup(t, r)
		rebuild(t, conn, r)
		empty := plus(jan1, 40)
		insertSource(t, conn, r, empty, "ECB", "EUR", "JPY", 150.0)
		refresh(t, conn, r, empty)
		if rows, ok := read(t, conn, r, empty, empty); ok {
			t.Errorf("read = %v", rows)
		}
	})
}

func TestRollupPopulatesMissingBucketsOnly(t *testing.T) {
	eachRollup(t, func(t *testing.T, r Rollup) {
		conn := groupedSetup(t, r)
		rebuild(t, conn, r)
		before := storedRate(t, conn, r, jan1, "EUR")
		other := plus(jan1, 40)
		insertSource(t, conn, r, other, "ECB", "USD", "EUR", 0.75)
		var calls []string
		withBatchHook(t, func(_ string, buckets []string) error {
			calls = append(calls, buckets...)
			return nil
		})
		populate(t, conn, r)
		if !slices.Equal(calls, []string{other}) {
			t.Errorf("refreshed %v", calls)
		}
		if got := storedRate(t, conn, r, jan1, "EUR"); got != before {
			t.Errorf("history rewritten: %v -> %v", before, got)
		}
		if got := storedRate(t, conn, r, other, "EUR"); got != 0.75 {
			t.Errorf("other EUR = %v", got)
		}
		if n := populate(t, conn, r); n != 0 {
			t.Errorf("second populate = %d", n)
		}
	})
}

func TestRollupPopulateReportsNothingForUncomputableBuckets(t *testing.T) {
	eachRollup(t, func(t *testing.T, r Rollup) {
		conn := groupedSetup(t, r)
		rebuild(t, conn, r)
		insertSource(t, conn, r, plus(jan1, 40), "ECB", "EUR", "JPY", 150.0)
		if n := populate(t, conn, r); n != 0 {
			t.Errorf("populate = %d", n)
		}
	})
}

func TestRollupRollsBackWhenBlendFails(t *testing.T) {
	eachRollup(t, func(t *testing.T, r Rollup) {
		conn := groupedSetup(t, r)
		rebuild(t, conn, r)
		before := snapshot(t, conn, r.Table, "")
		exec(t, conn, "UPDATE "+r.Source.Name+" SET rate = 9.0 WHERE quote = 'CHF'")
		withBatchHook(t, func(string, []string) error { return errors.New("failed blend") })
		if _, err := r.Refresh(ctx, conn, []string{jan1}, today()); err == nil {
			t.Fatal("no error")
		}
		if got := snapshot(t, conn, r.Table, ""); !slices.Equal(got, before) {
			t.Errorf("rows changed: %v -> %v", before, got)
		}
	})
}

func TestRollupRestoresRowsWhenInsertFailsAfterDelete(t *testing.T) {
	eachRollup(t, func(t *testing.T, r Rollup) {
		conn := groupedSetup(t, r)
		rebuild(t, conn, r)
		before := snapshot(t, conn, r.Table, "")
		exec(t, conn, "UPDATE "+r.Source.Name+" SET rate = 9.0 WHERE quote = 'CHF'")
		// The trigger reports whether the old bucket was already deleted when the insert arrived.
		exec(t, conn, "CREATE TRIGGER fail_insert BEFORE INSERT ON "+r.Table+" BEGIN SELECT CASE WHEN (SELECT count(*) FROM "+
			r.Table+" WHERE bucket_date = '"+jan1+"') = 0 THEN RAISE(ABORT, 'failed insert after delete') ELSE "+
			"RAISE(ABORT, 'failed insert before delete') END; END")
		_, err := r.Refresh(ctx, conn, []string{jan1}, today())
		if err == nil || !strings.Contains(err.Error(), "after delete") {
			t.Fatalf("err = %v", err)
		}
		exec(t, conn, "DROP TRIGGER fail_insert")
		if got := snapshot(t, conn, r.Table, ""); !slices.Equal(got, before) {
			t.Errorf("rows changed: %v -> %v", before, got)
		}
	})
}

func TestRollupBoundsSourceReads(t *testing.T) {
	eachRollup(t, func(t *testing.T, r Rollup) {
		conn := groupedSetup(t, r)
		exec(t, conn, "DELETE FROM "+r.Source.Name)
		var dates []string
		for i := range 205 {
			dates = append(dates, plus(jan1, i*7))
			insertSource(t, conn, r, dates[i], "ECB", "USD", "EUR", 0.8)
		}
		var sizes []int
		withBatchHook(t, func(_ string, buckets []string) error {
			sizes = append(sizes, len(buckets))
			return nil
		})
		refresh(t, conn, r, dates...)
		if len(sizes) == 0 || slices.Max(sizes) > BatchBuckets {
			t.Errorf("batch sizes = %v", sizes)
		}
		if n := queryInt(t, conn, "SELECT count(*) FROM "+r.Table+" WHERE quote = 'EUR'"); n != len(dates) {
			t.Errorf("EUR rows = %d", n)
		}
	})
}

// Grouped blend ingestion. Provider#backfill belongs to the provider step; ingest reproduces the transaction it runs
// per fetched batch, so these cases check that the grouped refreshes join it, fail it and roll back with it.
func ingest(t *testing.T, conn *sql.DB, provider string, records []adapter.Rate, purge func()) error {
	t.Helper()
	inserted := 0
	err := db.Immediate(ctx, conn, func(q db.Querier) error {
		var dates []time.Time
		var codes []string
		for _, r := range records {
			c := rates.ComponentsOf(r)
			res, err := q.ExecContext(ctx, "INSERT INTO rates (provider, date, base, quote, mid, bid, ask) VALUES "+
				"(?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING", provider, day(r.Date), r.Base, r.Quote, c.Mid, c.Bid, c.Ask)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			inserted += int(n)
			dates = append(dates, r.Date)
			codes = append(codes, r.Base, r.Quote)
		}
		if inserted == 0 {
			return nil
		}
		if err := RefreshProviderRollups(ctx, q, provider, dates, today()); err != nil {
			return err
		}
		if err := rates.RefreshSummaries(ctx, q, uniq(codes), provider); err != nil {
			return err
		}
		ok, err := blends(ctx, q, provider)
		if err != nil || !ok {
			return err
		}
		lo, hi := slices.MinFunc(dates, time.Time.Compare), slices.MaxFunc(dates, time.Time.Compare)
		return RefreshDaily(ctx, q, lo, hi.AddDate(0, 0, rates.LookbackDays), today())
	})
	if err == nil && inserted > 0 && purge != nil {
		purge()
	}
	return err
}

func ingestionSetup(t *testing.T) (*sql.DB, time.Time, []adapter.Rate) {
	conn := fixtures.New(t)
	for _, r := range Rollups {
		rebuild(t, conn, r)
	}
	date := fixtures.BusinessDay(60)
	records := []adapter.Rate{{Date: date, Base: "EUR", Quote: "USD", Rate: 1.3, Bid: adapter.Float(1.2),
		Ask: adapter.Float(1.4)}}
	return conn, date, records
}

func bucketOf(t *testing.T, q db.Querier, p rates.Precision, date time.Time) string {
	t.Helper()
	return queryDate(t, q, "SELECT "+rates.BucketSQL(p, db.LitDate(date)))
}

func TestIngestionRefreshesAffectedBucketsBeforePurge(t *testing.T) {
	conn, date, records := ingestionSetup(t)
	buckets := map[string]string{}
	before := map[string][]string{}
	for _, r := range Rollups {
		buckets[r.Table] = bucketOf(t, conn, r.Source.Precision, date)
		before[r.Table] = snapshot(t, conn, r.Table, "bucket_date != ?", buckets[r.Table])
	}
	purged := false
	err := ingest(t, conn, "BCB", records, func() {
		for _, r := range Rollups {
			stored := storedRate(t, conn, r, buckets[r.Table], "EUR")
			// The new provider changes the EUR blend in the bucket; compare to a fresh full-source refresh.
			refresh(t, conn, r, buckets[r.Table])
			if fresh := storedRate(t, conn, r, buckets[r.Table], "EUR"); fresh != stored {
				t.Errorf("%s: stored %v, fresh %v", r.Table, stored, fresh)
			}
		}
		purged = true
	})
	if err != nil {
		t.Fatal(err)
	}
	if !purged {
		t.Error("no purge")
	}

	var mid sql.NullFloat64
	var bid, ask, rate float64
	if err := conn.QueryRowContext(ctx, "SELECT mid, bid, ask, rate FROM rates WHERE provider = 'BCB' AND date = ? AND "+
		"base = 'EUR' AND quote = 'USD'", day(date)).Scan(&mid, &bid, &ask, &rate); err != nil {
		t.Fatal(err)
	}
	if mid.Valid || bid != 1.2 || ask != 1.4 || rate != 1.3 {
		t.Errorf("stored %v %v %v %v", mid, bid, ask, rate)
	}
	for _, r := range Rollups {
		got := queryFloat(t, conn, "SELECT rate FROM "+r.Source.Name+" WHERE provider = 'BCB' AND bucket_date = ? AND "+
			"base = 'EUR' AND quote = 'USD'", buckets[r.Table])
		if got != 1.3 {
			t.Errorf("%s = %v", r.Source.Name, got)
		}
		if after := snapshot(t, conn, r.Table, "bucket_date != ?", buckets[r.Table]); !slices.Equal(after, before[r.Table]) {
			t.Errorf("%s: other buckets changed", r.Table)
		}
	}
}

func TestIngestionRollsBackWhenGroupedRefreshFails(t *testing.T) {
	conn, date, records := ingestionSetup(t)
	weekly, monthly := queryInt(t, conn, "SELECT count(*) FROM weekly_rates"), queryInt(t, conn,
		"SELECT count(*) FROM monthly_rates")
	before := map[string][]string{}
	for _, r := range Rollups {
		before[r.Table] = snapshot(t, conn, r.Table, "")
	}
	withBatchHook(t, func(table string, _ []string) error {
		if table == Monthly.Table {
			return errors.New("failed grouped refresh")
		}
		return nil
	})
	purged := false
	if err := ingest(t, conn, "BCB", records, func() { purged = true }); err == nil {
		t.Fatal("no error")
	}
	if n := queryInt(t, conn, "SELECT count(*) FROM rates WHERE provider = 'BCB' AND date = ?", day(date)); n != 0 {
		t.Errorf("rates = %d", n)
	}
	if n := queryInt(t, conn, "SELECT count(*) FROM weekly_rates"); n != weekly {
		t.Errorf("weekly = %d, want %d", n, weekly)
	}
	if n := queryInt(t, conn, "SELECT count(*) FROM monthly_rates"); n != monthly {
		t.Errorf("monthly = %d, want %d", n, monthly)
	}
	for _, r := range Rollups {
		if got := snapshot(t, conn, r.Table, ""); !slices.Equal(got, before[r.Table]) {
			t.Errorf("%s changed", r.Table)
		}
	}
	if purged {
		t.Error("purged")
	}
}

func TestIngestionOfNonBlendingProviderSkipsGroupedBlends(t *testing.T) {
	conn, date, records := ingestionSetup(t)
	called := false
	withBatchHook(t, func(string, []string) error {
		called = true
		return nil
	})
	if err := ingest(t, conn, "UST", records, nil); err != nil {
		t.Fatal(err)
	}
	if n := queryInt(t, conn, "SELECT count(*) FROM rates WHERE provider = 'UST' AND date = ?", day(date)); n != 1 {
		t.Errorf("rates = %d", n)
	}
	for _, table := range []string{"weekly_rates", "monthly_rates"} {
		if n := queryInt(t, conn, "SELECT count(*) FROM "+table+" WHERE provider = 'UST'"); n == 0 {
			t.Errorf("%s has no UST rows", table)
		}
	}
	if called {
		t.Error("grouped blends recomputed")
	}
}

func TestIngestionRollsBackEarlierBatchesWhenLaterFails(t *testing.T) {
	conn, _, _ := ingestionSetup(t)
	before := map[string][]string{}
	for _, r := range Rollups {
		before[r.Table] = snapshot(t, conn, r.Table, "")
	}
	weekly, monthly := queryInt(t, conn, "SELECT count(*) FROM weekly_rates"), queryInt(t, conn,
		"SELECT count(*) FROM monthly_rates")
	var records []adapter.Rate
	for i := range 101 {
		records = append(records, adapter.Rate{Date: fixtures.LatestDate().AddDate(0, 0, -7*i), Base: "EUR", Quote: "USD",
			Rate: 1.3})
	}
	batches := 0
	withBatchHook(t, func(table string, _ []string) error {
		if table != Weekly.Table {
			return nil
		}
		batches++
		if batches == 2 {
			return errors.New("failed second batch")
		}
		return nil
	})
	purged := false
	if err := ingest(t, conn, "BCB", records, func() { purged = true }); err == nil {
		t.Fatal("no error")
	}
	if batches != 2 {
		t.Errorf("batches = %d", batches)
	}
	if n := queryInt(t, conn, "SELECT count(*) FROM rates WHERE provider = 'BCB'"); n != 0 {
		t.Errorf("rates = %d", n)
	}
	if w, m := queryInt(t, conn, "SELECT count(*) FROM weekly_rates"), queryInt(t, conn,
		"SELECT count(*) FROM monthly_rates"); w != weekly || m != monthly {
		t.Errorf("rollups %d/%d, want %d/%d", w, m, weekly, monthly)
	}
	for _, r := range Rollups {
		if got := snapshot(t, conn, r.Table, ""); !slices.Equal(got, before[r.Table]) {
			t.Errorf("%s changed", r.Table)
		}
	}
	if purged {
		t.Error("purged")
	}
}

func TestReadUsesOneSnapshot(t *testing.T) {
	conn := fixtures.New(t)
	insertSource(t, conn, Weekly, jan1, "ECB", "USD", "EUR", 0.8)
	refresh(t, conn, Weekly, jan1)

	changed := false
	original := afterRead
	afterRead = func() {
		if changed {
			return
		}
		changed = true
		// Another pooled connection commits a change between the coverage and value queries.
		if err := db.Immediate(ctx, conn, func(q db.Querier) error {
			if _, err := q.ExecContext(ctx, "UPDATE weekly_rates SET rate = 0.9 WHERE bucket_date = ?", jan1); err != nil {
				return err
			}
			_, err := q.ExecContext(ctx, "UPDATE blended_weekly_rates SET rate = 0.9 WHERE quote = 'EUR'")
			return err
		}); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { afterRead = original })

	rows, ok := read(t, conn, Weekly, jan1, jan1)
	if !changed || !ok {
		t.Fatalf("changed %v, ok %v", changed, ok)
	}
	if want := fmt.Sprint(jan1, "USD", "EUR", 0.8); !slices.Contains(rows, want) {
		t.Errorf("read %v, want %v among them", rows, want)
	}
	if got := storedRate(t, conn, Weekly, jan1, "EUR"); got != 0.9 {
		t.Errorf("committed EUR = %v", got)
	}
}
