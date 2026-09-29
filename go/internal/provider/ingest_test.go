package provider

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/blend"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// Grouped blend ingestion (spec/blended_rollup_spec.rb) against the real backfill and the real materialized blend.

func ingestionEnv(t *testing.T, key string, a adapter.Adapter) *env {
	t.Helper()
	e := newEnv(t, key, a)
	e.in.Blend = nil
	for _, r := range blend.Rollups {
		if err := r.Rebuild(context.Background(), e.conn, e.today); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func bidAskAdapter(date time.Time) *fakeAdapter {
	return returning(adapter.Rate{Date: date, Base: "EUR", Quote: "USD", Rate: 1.3, Bid: adapter.Float(1.2),
		Ask: adapter.Float(1.4)})
}

// tableSnapshot renders every row of table (optionally filtered) as text, in a stable order.
func tableSnapshot(t *testing.T, q db.Querier, table, where string, args ...any) []string {
	t.Helper()
	query := "SELECT * FROM " + table
	if where != "" {
		query += " WHERE " + where
	}
	rows, err := q.QueryContext(context.Background(), query+" ORDER BY 1, 2, 3", args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
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
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func scalar[T any](t *testing.T, q db.Querier, query string, args ...any) T {
	t.Helper()
	var v T
	if err := q.QueryRowContext(context.Background(), query, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return v
}

func bucketOf(t *testing.T, conn *sql.DB, p rates.Precision, date time.Time) string {
	t.Helper()
	return db.FormatDate(scalar[db.NullDate](t, conn, "SELECT "+rates.BucketSQL(p, db.LitDate(date))).Time)
}

// failInsertsInto makes inserts into table fail when the SQL condition when (which may refer to NEW) holds.
func failInsertsInto(t *testing.T, conn *sql.DB, table, when string) {
	t.Helper()
	if _, err := conn.Exec("CREATE TRIGGER fail_" + table + " BEFORE INSERT ON " + table + " WHEN " + when +
		" BEGIN SELECT RAISE(ABORT, 'failed grouped refresh'); END"); err != nil {
		t.Fatal(err)
	}
}

// purgeCheck runs check when the backfill purges.
type purgeCheck struct {
	check  func()
	purged bool
}

func (p *purgeCheck) PurgeDebounced(context.Context) error {
	if p.check != nil {
		p.check()
	}
	p.purged = true
	return nil
}

func TestIngestionRefreshesBothAffectedBucketsBeforePurgingAndLeavesOtherBucketsAlone(t *testing.T) {
	date := fixtures.BusinessDay(60)
	e := ingestionEnv(t, "BCB", bidAskAdapter(date))
	ctx := context.Background()
	buckets := map[string]string{}
	before := map[string][]string{}
	for _, r := range blend.Rollups {
		buckets[r.Table] = bucketOf(t, e.conn, r.Source.Precision, date)
		before[r.Table] = tableSnapshot(t, e.conn, r.Table, "bucket_date != ?", buckets[r.Table])
	}
	eurRate := func(r blend.Rollup) float64 {
		return scalar[float64](t, e.conn, "SELECT rate FROM "+r.Table+" WHERE bucket_date = ? AND quote = 'EUR'",
			buckets[r.Table])
	}
	purge := &purgeCheck{check: func() {
		for _, r := range blend.Rollups {
			stored := eurRate(r)
			// The new provider changes the EUR blend in the bucket; compare to a fresh full-source refresh.
			if _, err := r.Refresh(ctx, e.conn, []string{buckets[r.Table]}, e.today); err != nil {
				t.Fatal(err)
			}
			if fresh := eurRate(r); fresh != stored {
				t.Errorf("%s: stored %v, fresh %v", r.Table, stored, fresh)
			}
		}
	}}
	e.in.Cache = purge
	e.in.BackfillAfter(ctx, e.provider, date.AddDate(0, 0, -1))

	if !purge.purged {
		t.Fatal("no purge")
	}
	var mid sql.NullFloat64
	var bid, ask, rate float64
	if err := e.conn.QueryRow("SELECT mid, bid, ask, rate FROM rates WHERE provider = 'BCB' AND date = ? AND "+
		"base = 'EUR' AND quote = 'USD'", d(date)).Scan(&mid, &bid, &ask, &rate); err != nil {
		t.Fatal(err)
	}
	if mid.Valid || bid != 1.2 || ask != 1.4 || rate != 1.3 {
		t.Errorf("stored %v %v %v %v", mid, bid, ask, rate)
	}
	for _, r := range blend.Rollups {
		got := scalar[float64](t, e.conn, "SELECT rate FROM "+r.Source.Name+" WHERE provider = 'BCB' AND "+
			"bucket_date = ? AND base = 'EUR' AND quote = 'USD'", buckets[r.Table])
		if got != 1.3 {
			t.Errorf("%s = %v", r.Source.Name, got)
		}
		after := tableSnapshot(t, e.conn, r.Table, "bucket_date != ?", buckets[r.Table])
		if !slices.Equal(after, before[r.Table]) {
			t.Errorf("%s: other buckets changed", r.Table)
		}
	}
}

func TestIngestionRollsBackRawAndProviderRollupChangesWhenAGroupedRefreshFails(t *testing.T) {
	date := fixtures.BusinessDay(60)
	e := ingestionEnv(t, "BCB", bidAskAdapter(date))
	weekly := scalar[int](t, e.conn, "SELECT count(*) FROM weekly_rates")
	monthly := scalar[int](t, e.conn, "SELECT count(*) FROM monthly_rates")
	before := map[string][]string{}
	for _, table := range []string{"weekly_rates", "monthly_rates", blend.Weekly.Table, blend.Monthly.Table} {
		before[table] = tableSnapshot(t, e.conn, table, "")
	}
	failInsertsInto(t, e.conn, blend.Monthly.Table, "1")
	purge := &purgeCheck{}
	e.in.Cache = purge
	e.in.BackfillAfter(context.Background(), e.provider, date.AddDate(0, 0, -1))

	if n := e.count(t, "date = ?", d(date)); n != 0 {
		t.Errorf("rates = %d", n)
	}
	if n := scalar[int](t, e.conn, "SELECT count(*) FROM weekly_rates"); n != weekly {
		t.Errorf("weekly_rates = %d, want %d", n, weekly)
	}
	if n := scalar[int](t, e.conn, "SELECT count(*) FROM monthly_rates"); n != monthly {
		t.Errorf("monthly_rates = %d, want %d", n, monthly)
	}
	for table, prior := range before {
		if got := tableSnapshot(t, e.conn, table, ""); !slices.Equal(got, prior) {
			t.Errorf("%s changed", table)
		}
	}
	if purge.purged {
		t.Error("purged")
	}
	if errs := e.log.at(slog.LevelError); len(errs) != 1 || !strings.Contains(errs[0].Attrs["error"], "failed grouped refresh") {
		t.Errorf("errors %+v", errs)
	}
}

func TestIngestionUpdatesNonBlendingProviderRollupsWithoutRecomputingBlendedTables(t *testing.T) {
	date := fixtures.BusinessDay(60)
	e := ingestionEnv(t, "UST", bidAskAdapter(date))
	if e.provider.Blends() {
		t.Fatal("UST blends")
	}
	for _, r := range blend.Rollups {
		failInsertsInto(t, e.conn, r.Table, "1")
	}
	e.in.BackfillAfter(context.Background(), e.provider, date.AddDate(0, 0, -1))

	if n := e.count(t, "date = ?", d(date)); n != 1 {
		t.Errorf("rates = %d", n)
	}
	for _, table := range []string{"weekly_rates", "monthly_rates"} {
		if n := scalar[int](t, e.conn, "SELECT count(*) FROM "+table+" WHERE provider = 'UST'"); n == 0 {
			t.Errorf("%s has no UST rows", table)
		}
	}
	if errs := e.log.at(slog.LevelError); len(errs) != 0 {
		t.Errorf("grouped blends recomputed: %+v", errs)
	}
}

func TestIngestionRollsBackEarlierGroupedBatchesAndSourceInsertsWhenALaterBatchFails(t *testing.T) {
	var records []adapter.Rate
	for i := range blend.BatchBuckets + 1 {
		records = append(records, rate(fixtures.LatestDate().AddDate(0, 0, -7*i), "EUR", "USD", 1.3))
	}
	e := ingestionEnv(t, "BCB", returning(records...))
	before := map[string][]string{}
	for _, table := range []string{"weekly_rates", "monthly_rates", blend.Weekly.Table, blend.Monthly.Table} {
		before[table] = tableSnapshot(t, e.conn, table, "")
	}
	// Buckets refresh oldest first, 100 to a batch, so the newest week is alone in the second batch.
	newest := bucketOf(t, e.conn, rates.Week, fixtures.LatestDate())
	failInsertsInto(t, e.conn, blend.Weekly.Table, "NEW.bucket_date = "+db.Lit(newest))
	purge := &purgeCheck{}
	e.in.Cache = purge
	e.in.BackfillAfter(context.Background(), e.provider, records[len(records)-1].Date.AddDate(0, 0, -1))

	if n := scalar[int](t, e.conn, "SELECT count(*) FROM rates WHERE provider = 'BCB'"); n != 0 {
		t.Errorf("rates = %d", n)
	}
	for table, prior := range before {
		if got := tableSnapshot(t, e.conn, table, ""); !slices.Equal(got, prior) {
			t.Errorf("%s changed", table)
		}
	}
	if purge.purged {
		t.Error("purged")
	}
	if errs := e.log.at(slog.LevelError); len(errs) != 1 || !strings.Contains(errs[0].Attrs["error"], "failed grouped refresh") {
		t.Errorf("errors %+v", errs)
	}
}
