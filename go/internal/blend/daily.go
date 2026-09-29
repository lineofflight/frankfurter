package blend

import (
	"context"
	"fmt"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// The daily materialized blend (blended_rates, #570) is the deduped blended series in the pivot base: one row per
// quote and date where the contributor set changed, peg anchoring included. It answers every plain request shape
// (latest, single date, daily range); what remains per request is deriving the requested base, filtering quotes, the
// identity row and rounding.
//
// Each stored row is the canonical anchor-date value: the blend computed at the anchor equal to the row's own
// observation date. Later anchors can re-emit the same quote and date with different floats as other contributors age
// out of the carry-forward lookback; those echoes are never stored, which keeps a stored row a pure function of its
// contributor rows.

// ChunkMonths is how many months RefreshDaily recomputes per transaction.
const ChunkMonths = 3

type span struct{ start, end time.Time }

func chunks(start, end time.Time) []span {
	var out []span
	for cursor := start; !cursor.After(end); {
		chunkEnd := addMonths(cursor, ChunkMonths).AddDate(0, 0, -1)
		if chunkEnd.After(end) {
			chunkEnd = end
		}
		out = append(out, span{cursor, chunkEnd})
		cursor = chunkEnd.AddDate(0, 0, 1)
	}
	return out
}

// RefreshDaily is BlendedRate.refresh: it recomputes the stored blends of every anchor date in [start, end]. It
// mirrors the live pipeline (same rows, EachSnapshot, Blend, AnchorPegs, same order) so stored values keep float
// parity with live computation.
func RefreshDaily(ctx context.Context, q db.Querier, start, end, today time.Time) error {
	for _, c := range chunks(start, end) {
		if err := refreshChunk(ctx, q, c, today); err != nil {
			return err
		}
	}
	return nil
}

// refreshChunk runs under BEGIN IMMEDIATE, which serializes the fetch, compute and write against other writers, so a
// refresh never commits blends computed from a snapshot another backfill has since changed. Inside a backfill's
// transaction it simply joins, since that transaction already holds the write lock.
func refreshChunk(ctx context.Context, q db.Querier, c span, today time.Time) error {
	return within(ctx, q, false, func(q db.Querier) error {
		scope, err := blendable(ctx, q, rates.Daily)
		if err != nil {
			return err
		}
		lookbackStart := c.start.AddDate(0, 0, -rates.LookbackDays)
		rows, err := rates.Select(ctx, q, scope.Filter("date >= "+db.LitDate(lookbackStart)+" AND date <= "+
			db.LitDate(c.end)).Columns("date, base, quote, provider, rate").SQL())
		if err != nil {
			return fmt.Errorf("refresh blended_rates: %w", err)
		}
		var anchors []time.Time
		seen := map[int64]bool{}
		for _, r := range rows {
			if !r.Date.Before(c.start) && !r.Date.After(c.end) && !seen[r.Date.Unix()] {
				seen[r.Date.Unix()] = true
				anchors = append(anchors, r.Date)
			}
		}

		var buffer []stored
		rates.EachSnapshot(rows, anchors, rates.LookbackDays, func(anchor time.Time, contributors []rates.Row) {
			if len(contributors) == 0 {
				return
			}
			for _, r := range currency.AnchorPegs(Blend(contributors, Pivot, today), Pivot) {
				if r.Date.Equal(anchor) {
					buffer = append(buffer, stored{db.FormatDate(r.Date), r.Quote, r.Rate})
				}
			}
		})

		if _, err := q.ExecContext(ctx, "DELETE FROM blended_rates WHERE date >= ? AND date <= ?",
			db.FormatDate(c.start), db.FormatDate(c.end)); err != nil {
			return err
		}
		return insert(ctx, q, "blended_rates", "date", buffer)
	})
}

// RebuildDaily is BlendedRate.rebuild. It rebuilds in place, newest chunk first, so existing chunks stay readable
// throughout and DailyReady stays true. Stale leading rows are pruned up front, so a shrunk active range keeps the
// table ready at once; a final sweep prunes rows outside the active date range without racing concurrent backfills.
func RebuildDaily(ctx context.Context, q db.Querier, today time.Time) error {
	var window *span
	err := within(ctx, q, false, func(q db.Querier) error {
		scope, err := blendable(ctx, q, rates.Daily)
		if err != nil {
			return err
		}
		first, err := scalarDate(ctx, q, scope.Columns("min(date)").SQL())
		if err != nil {
			return err
		}
		if !first.Valid {
			_, err := q.ExecContext(ctx, "DELETE FROM blended_rates")
			return err
		}
		if _, err := q.ExecContext(ctx, "DELETE FROM blended_rates WHERE date < ?", db.FormatDate(first.Time)); err != nil {
			return err
		}
		last, err := scalarDate(ctx, q, scope.Columns("max(date)").SQL())
		if err != nil {
			return err
		}
		window = &span{first.Time, last.Time}
		return nil
	})
	if err != nil || window == nil {
		return err
	}

	all := chunks(window.start, window.end)
	for i := len(all) - 1; i >= 0; i-- {
		if err := wrapChunk(func() error { return refreshChunk(ctx, q, all[i], today) }); err != nil {
			return err
		}
	}

	return within(ctx, q, false, func(q db.Querier) error {
		scope, err := blendable(ctx, q, rates.Daily)
		if err != nil {
			return err
		}
		var lo, hi db.NullDate
		if err := q.QueryRowContext(ctx, scope.Columns("min(date), max(date)").SQL()).Scan(&lo, &hi); err != nil {
			return err
		}
		if !lo.Valid {
			return nil
		}
		_, err = q.ExecContext(ctx, "DELETE FROM blended_rates WHERE NOT (date >= ? AND date <= ?)",
			db.FormatDate(lo.Time), db.FormatDate(hi.Time))
		return err
	})
}

// DailyReady is BlendedRate.ready?: the table serves reads only once it covers full history. An incremental refresh
// makes it non-empty long before a rebuild has run, and serving a partial table would silently truncate historical
// ranges.
func DailyReady(ctx context.Context, q db.Querier) (bool, error) {
	scope, err := blendable(ctx, q, rates.Daily)
	if err != nil {
		return false, err
	}
	first, err := scalarDate(ctx, q, scope.Columns("min(date)").SQL())
	if err != nil || !first.Valid {
		return false, err
	}
	stored, err := scalarDate(ctx, q, "SELECT min(date) FROM blended_rates")
	if err != nil {
		return false, err
	}
	return stored.Valid && stored.Time.Equal(first.Time), nil
}
