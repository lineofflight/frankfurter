package blend

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// Rollup is a materialized grouped blend (BlendedRollup): the blend of a
// provider rollup table's rows per bucket, not an average of daily blends.
// Every bucket keeps its full input set and unrounded pivot values. Empty
// blends stay empty: reads fall back for those buckets rather than
// manufacturing an identity row or choosing an older bucket.
type Rollup struct {
	Table  string      // blended_weekly_rates or blended_monthly_rates
	Source rates.Table // the provider rollup it blends
}

// The grouped blends.
var (
	Weekly  = Rollup{"blended_weekly_rates", rates.Weekly}
	Monthly = Rollup{"blended_monthly_rates", rates.Monthly}

	Rollups = []Rollup{Weekly, Monthly}
)

// BatchBuckets bounds how many buckets one refresh transaction (or savepoint)
// loads.
const BatchBuckets = 100

// Refresh recomputes the stored blends of buckets (stored date text) and
// returns how many rows it wrote. Callers include whole-history adapter
// batches, so buckets load in bounded batches; inside an ingestion transaction
// each batch joins it, so every source insert and grouped replacement still
// rolls back together on failure.
func (r Rollup) Refresh(ctx context.Context, q db.Querier, buckets []string, today time.Time) (int, error) {
	buckets = uniq(buckets)
	total := 0
	for start := 0; start < len(buckets); start += BatchBuckets {
		n, err := r.refreshBatch(ctx, q, buckets[start:min(start+BatchBuckets, len(buckets))], today)
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

func (r Rollup) refreshBatch(ctx context.Context, q db.Querier, buckets []string, today time.Time) (int, error) {
	var written int
	err := within(ctx, q, true, func(q db.Querier) error {
		if err := beforeBatch(r.Table, buckets); err != nil {
			return err
		}
		scope, err := blendable(ctx, q, r.Source)
		if err != nil {
			return err
		}
		list := db.LitList(buckets)
		rows, err := rates.Select(ctx, q, scope.Filter("bucket_date IN "+list).
			Columns("bucket_date, base, quote, provider, rate").OrderBy("bucket_date, quote").SQL())
		if err != nil {
			return fmt.Errorf("refresh %s: %w", r.Table, err)
		}

		var records []stored
		for start := 0; start < len(rows); {
			end := start + 1
			for end < len(rows) && rows[end].Date.Equal(rows[start].Date) {
				end++
			}
			bucket := db.FormatDate(rows[start].Date)
			for _, b := range currency.AnchorPegs(Blend(rows[start:end], Pivot, today), Pivot) {
				records = append(records, stored{bucket, b.Quote, b.Rate})
			}
			start = end
		}

		if _, err := q.ExecContext(ctx, "DELETE FROM "+r.Table+" WHERE bucket_date IN "+list); err != nil {
			return err
		}
		if err := insert(ctx, q, r.Table, "bucket_date", records); err != nil {
			return err
		}
		written = len(records)
		return nil
	})
	return written, err
}

// Rebuild recomputes every bucket, newest first, then drops stored buckets
// whose source rows are gone.
func (r Rollup) Rebuild(ctx context.Context, q db.Querier, today time.Time) error {
	scope, err := blendable(ctx, q, r.Source)
	if err != nil {
		return err
	}
	dates, err := bucketDates(ctx, q, scope.Columns("DISTINCT bucket_date").OrderBy("bucket_date").SQL())
	if err != nil {
		return err
	}
	slices.Reverse(dates)
	if _, err := r.Refresh(ctx, q, dates, today); err != nil {
		return err
	}
	return within(ctx, q, false, func(q db.Querier) error {
		scope, err := blendable(ctx, q, r.Source)
		if err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, "DELETE FROM "+r.Table+" WHERE NOT (bucket_date IN ("+
			scope.Columns("bucket_date").SQL()+"))")
		return err
	})
}

func (r Rollup) missing(scope rates.Query) rates.Query {
	return scope.Filter("NOT (bucket_date IN (SELECT bucket_date FROM " + r.Table + "))")
}

// Populate fills incomplete builds without rewriting covered history, and
// returns how many rows it wrote. Buckets with a legitimately empty pivot blend
// stay missing forever and are retried each time; they write nothing, so a zero
// return means nothing changed and no cache purge is needed.
func (r Rollup) Populate(ctx context.Context, q db.Querier, today time.Time) (int, error) {
	scope, err := blendable(ctx, q, r.Source)
	if err != nil {
		return 0, err
	}
	dates, err := bucketDates(ctx, q, r.missing(scope).Columns("DISTINCT bucket_date").OrderBy("bucket_date").SQL())
	if err != nil {
		return 0, err
	}
	slices.Reverse(dates)
	return r.Refresh(ctx, q, dates, today)
}

// Ready reports whether every source bucket has stored blends.
func (r Rollup) Ready(ctx context.Context, q db.Querier) (bool, error) {
	scope, err := blendable(ctx, q, r.Source)
	if err != nil {
		return false, err
	}
	var one int
	err = q.QueryRowContext(ctx, r.missing(scope).Columns("1").SQL()+" LIMIT 1").Scan(&one)
	if err == sql.ErrNoRows {
		return true, nil
	}
	return false, err
}

// Read returns the stored blends (in the pivot base, ordered by bucket and
// quote) for the source buckets Between selects over [start, end]. Source
// buckets decide snap-back, including buckets that produce no pivot blend. ok
// is false when any of them has no stored row, so the caller falls back to live
// computation; a partial build still serves complete ranges. Coverage and
// values come from one read snapshot, so concurrent ingestion or a rebuild
// never mixes old coverage with new rows.
func (r Rollup) Read(ctx context.Context, q db.Querier, start, end, today time.Time) (rows []currency.Blended, ok bool, err error) {
	if conn, isDB := q.(*sql.DB); isDB {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return nil, false, err
		}
		defer tx.Rollback()
		return r.read(ctx, tx, start, end, today)
	}
	return r.read(ctx, q, start, end, today)
}

func (r Rollup) read(ctx context.Context, q db.Querier, start, end, today time.Time) ([]currency.Blended, bool, error) {
	scope, err := blendable(ctx, q, r.Source)
	if err != nil {
		return nil, false, err
	}
	dates, err := bucketDates(ctx, q, scope.Between(start, end, today).Columns("DISTINCT bucket_date").
		OrderBy("bucket_date").SQL())
	if err != nil {
		return nil, false, err
	}
	afterRead()
	result, err := q.QueryContext(ctx, "SELECT bucket_date, quote, rate FROM "+r.Table+" WHERE bucket_date IN "+
		db.LitList(dates)+" ORDER BY bucket_date, quote")
	if err != nil {
		return nil, false, err
	}
	defer result.Close()
	out := []currency.Blended{}
	covered := map[string]bool{}
	for result.Next() {
		var d db.NullDate
		b := currency.Blended{Base: Pivot}
		if err := result.Scan(&d, &b.Quote, &b.Rate); err != nil {
			return nil, false, err
		}
		b.Date = d.Time
		covered[db.FormatDate(d.Time)] = true
		out = append(out, b)
	}
	if err := result.Err(); err != nil {
		return nil, false, err
	}
	for _, d := range dates {
		if !covered[d] {
			return nil, false, nil
		}
	}
	return out, true, nil
}
