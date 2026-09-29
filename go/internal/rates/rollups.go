package rates

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/db"
)

// RefreshRollups is the provider half of Provider#refresh_rollups: for each rollup table it rebuilds provider's
// buckets that contain any of dates from its daily rows, and returns the buckets touched per precision (bucket dates as
// stored text). Refreshing the blended rollups for those buckets is the caller's job.
func RefreshRollups(ctx context.Context, q db.Querier, provider string, dates []time.Time) (map[Precision][]string, error) {
	touched := map[Precision][]string{}
	if len(dates) == 0 {
		return touched, nil
	}
	list := make([]string, len(dates))
	for i, d := range dates {
		list[i] = db.FormatDate(d)
	}
	for _, t := range Rollups {
		bucket := BucketSQL(t.Precision, "date")
		rows, err := q.QueryContext(ctx, "SELECT DISTINCT "+bucket+" FROM rates WHERE provider = ? AND date IN "+
			db.LitList(list), provider)
		if err != nil {
			return nil, fmt.Errorf("refresh %s: %w", t.Name, err)
		}
		var buckets []string
		for rows.Next() {
			var b string
			if err := rows.Scan(&b); err != nil {
				rows.Close()
				return nil, err
			}
			buckets = append(buckets, b)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if len(buckets) == 0 {
			continue
		}
		sort.Strings(buckets)
		if err := rebuildBuckets(ctx, q, t, provider, buckets); err != nil {
			return nil, err
		}
		touched[t.Precision] = buckets
	}
	return touched, nil
}

// RebuildRollups recomputes every provider rollup table from the daily rates.
func RebuildRollups(ctx context.Context, q db.Querier) error {
	for _, t := range Rollups {
		bucket := BucketSQL(t.Precision, "date")
		if _, err := q.ExecContext(ctx, "DELETE FROM "+t.Name); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, "INSERT INTO "+t.Name+" (bucket_date, provider, base, quote, rate) SELECT "+
			bucket+", provider, base, quote, avg(rate) FROM rates GROUP BY provider, base, quote, "+bucket); err != nil {
			return fmt.Errorf("rebuild %s: %w", t.Name, err)
		}
	}
	return nil
}
