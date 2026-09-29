package blend

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// blends reports whether provider's rows enter the blend (Provider#blends?).
func blends(ctx context.Context, q db.Querier, provider string) (bool, error) {
	nonBlending, err := rates.NonBlendingKeys(ctx, q)
	if err != nil {
		return false, err
	}
	return !slices.Contains(nonBlending, provider), nil
}

// RefreshProviderRollups is Provider#refresh_rollups: it rebuilds provider's weekly and monthly buckets holding any of
// dates and, when the provider blends, the grouped blends of those buckets. Backfill calls it inside its insert
// transaction, so everything rolls back together.
func RefreshProviderRollups(ctx context.Context, q db.Querier, provider string, dates []time.Time, today time.Time) error {
	touched, err := rates.RefreshRollups(ctx, q, provider, dates)
	if err != nil {
		return err
	}
	ok, err := blends(ctx, q, provider)
	if err != nil || !ok {
		return err
	}
	for _, r := range Rollups {
		if _, err := r.Refresh(ctx, q, touched[r.Source.Precision], today); err != nil {
			return err
		}
	}
	return nil
}

// RebuildAll is the blend:rebuild task: it rebuilds the daily, weekly and monthly materialized blends in place.
// Rebuilds change served values, so the caller must purge cached responses afterwards.
func RebuildAll(ctx context.Context, conn *sql.DB, today time.Time) error {
	steps := []struct {
		table   string
		rebuild func() error
	}{
		{"blended_rates", func() error { return RebuildDaily(ctx, conn, today) }},
		{Weekly.Table, func() error { return Weekly.Rebuild(ctx, conn, today) }},
		{Monthly.Table, func() error { return Monthly.Rebuild(ctx, conn, today) }},
	}
	for _, s := range steps {
		started := time.Now()
		if err := s.rebuild(); err != nil {
			return fmt.Errorf("blend:rebuild %s: %w", s.table, err)
		}
		n, err := count(ctx, conn, s.table)
		if err != nil {
			return err
		}
		slog.Info("blend:rebuild", "table", s.table, "rows", n, "seconds", time.Since(started).Round(100*time.Millisecond).Seconds())
	}
	return nil
}

// RebuildProviderRollups is the rollups:rebuild task: it rebuilds the weekly and monthly provider rollups from the
// daily rates, for one provider (matched case-insensitively) or all when provider is empty. The source rebuild and
// the invalidation of affected grouped blends commit together; the grouped blends are refilled afterwards, outside
// that transaction, so a refill failure leaves only the affected buckets on the live fallback. The caller must purge
// the cache even when this returns an error, since the source changes may have committed.
func RebuildProviderRollups(ctx context.Context, conn *sql.DB, provider string, today time.Time) error {
	scope, label := "1", "all"
	if provider != "" {
		var key string
		err := conn.QueryRowContext(ctx, "SELECT key FROM providers WHERE lower(key) = lower(?)", provider).Scan(&key)
		if err == sql.ErrNoRows {
			return fmt.Errorf("unknown provider: %s", provider)
		}
		if err != nil {
			return err
		}
		scope, label = "provider = "+db.Lit(key), key
	}

	affected := map[string][]string{}
	err := within(ctx, conn, true, func(q db.Querier) error {
		sources := map[string]rates.Query{}
		for _, r := range Rollups {
			s, err := blendable(ctx, q, r.Source)
			if err != nil {
				return err
			}
			sources[r.Table] = s.Filter(scope).Columns("DISTINCT bucket_date")
			if affected[r.Table], err = bucketDates(ctx, q, sources[r.Table].SQL()); err != nil {
				return err
			}
		}
		for _, t := range rates.Rollups {
			if _, err := q.ExecContext(ctx, "DELETE FROM "+t.Name+" WHERE "+scope); err != nil {
				return err
			}
		}
		for _, t := range rates.Rollups {
			bucket := rates.BucketSQL(t.Precision, "date")
			if _, err := q.ExecContext(ctx, "INSERT INTO "+t.Name+" (bucket_date, provider, base, quote, rate) SELECT "+
				bucket+", provider, base, quote, avg(rate) FROM rates WHERE "+scope+" GROUP BY provider, base, quote, "+
				bucket); err != nil {
				return fmt.Errorf("rebuild %s: %w", t.Name, err)
			}
		}
		for _, r := range Rollups {
			dates, err := bucketDates(ctx, q, sources[r.Table].SQL())
			if err != nil {
				return err
			}
			// Include buckets the rebuild removed, and invalidate before releasing the source write lock.
			affected[r.Table] = uniq(append(affected[r.Table], dates...))
			if _, err := q.ExecContext(ctx, "DELETE FROM "+r.Table+" WHERE bucket_date IN "+
				db.LitList(affected[r.Table])); err != nil {
				return err
			}
		}
		var weekly, monthly int
		if err := q.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM weekly_rates WHERE "+scope+
			"), (SELECT count(*) FROM monthly_rates WHERE "+scope+")").Scan(&weekly, &monthly); err != nil {
			return err
		}
		slog.Info("rollups:rebuild", "provider", label, "weekly", weekly, "monthly", monthly)
		return nil
	})
	if err != nil {
		return err
	}

	for _, r := range Rollups {
		if len(affected[r.Table]) == 0 {
			continue
		}
		if _, err := r.Refresh(ctx, conn, affected[r.Table], today); err != nil {
			return err
		}
	}
	return nil
}

// PurgeInvalid is the db:purge_invalid task: rates.Purge, then, when anything was deleted, the grouped blends are
// repopulated (invalidation is bucket-local, so this is quick) before the slower daily rebuild. The caller must purge
// the cache whenever the totals are non-zero, even if a rebuild failed: the deletion has committed.
func PurgeInvalid(ctx context.Context, conn *sql.DB, today time.Time, leads map[string]int) (rates.PurgeTotals, error) {
	totals, err := rates.Purge(ctx, conn, today, leads)
	if err != nil {
		return totals, err
	}
	slog.Info("purge_invalid", "rates", totals.Rates, "weekly", totals.Weekly, "monthly", totals.Monthly)
	if totals.Total() == 0 {
		return totals, nil
	}
	for _, r := range Rollups {
		if _, err := r.Populate(ctx, conn, today); err != nil {
			return totals, err
		}
	}
	return totals, RebuildDaily(ctx, conn, today)
}

// OutlierCount is how often one provider and quote pair was flagged.
type OutlierCount struct {
	Provider, Quote string
	Count           int
}

// ConsensusReport is what the consensus tasks found.
type ConsensusReport struct {
	Counts []OutlierCount // most frequent first
	Total  int
	Dates  int
}

// ScanConsensus is the consensus task: for each date in [from, to] with rates, it blends that date's rows to EUR and
// counts the outliers per provider and quote. A zero from or to means the first or last stored date (all history).
// Results are logged as the task does and returned.
func ScanConsensus(ctx context.Context, q db.Querier, from, to, today time.Time) (ConsensusReport, error) {
	var report ConsensusReport
	if from.IsZero() || to.IsZero() {
		var lo, hi db.NullDate
		if err := q.QueryRowContext(ctx, "SELECT min(date), max(date) FROM rates").Scan(&lo, &hi); err != nil {
			return report, err
		}
		if from.IsZero() {
			from = lo.Time
		}
		if to.IsZero() {
			to = hi.Time
		}
	}
	rows, err := rates.Select(ctx, q, "SELECT date, base, quote, provider, rate FROM rates WHERE date >= ? AND date <= ?"+
		" ORDER BY date", db.FormatDate(from), db.FormatDate(to))
	if err != nil {
		return report, err
	}

	counts := map[[2]string]int{}
	for start := 0; start < len(rows); {
		end := start + 1
		for end < len(rows) && rows[end].Date.Equal(rows[start].Date) {
			end++
		}
		report.Dates++
		for _, o := range Outliers(rows[start:end], "EUR") {
			counts[[2]string{o.Provider, o.Quote}]++
			report.Total++
		}
		start = end
	}

	for k, n := range counts {
		report.Counts = append(report.Counts, OutlierCount{k[0], k[1], n})
	}
	sort.Slice(report.Counts, func(i, j int) bool {
		a, b := report.Counts[i], report.Counts[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Provider+" "+a.Quote < b.Provider+" "+b.Quote
	})
	for _, c := range report.Counts {
		slog.Info(fmt.Sprintf("%-25s %d", c.Provider+" "+c.Quote, c.Count))
	}
	slog.Info(fmt.Sprintf("Total: %d outliers across %d dates", report.Total, report.Dates))
	return report, nil
}

// ScanRecentConsensus is the consensus:recent task: ScanConsensus over the last 365 days.
func ScanRecentConsensus(ctx context.Context, q db.Querier, today time.Time) (ConsensusReport, error) {
	return ScanConsensus(ctx, q, today.AddDate(0, 0, -365), today, today)
}

// ScanYearConsensus is the consensus[year] task.
func ScanYearConsensus(ctx context.Context, q db.Querier, year int, today time.Time) (ConsensusReport, error) {
	return ScanConsensus(ctx, q, time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(year, 12, 31, 0, 0, 0, 0,
		time.UTC), today)
}
