package rates

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/db"
)

// RefreshRollups is the provider half of Provider#refresh_rollups: for each
// rollup table it rebuilds provider's buckets that contain any of dates from
// its daily rows, and returns the buckets touched per precision (bucket dates
// as stored text). Refreshing the blended rollups for those buckets is the
// caller's job.
func RefreshRollups(ctx context.Context, q db.Querier, provider string, dates []time.Time) (map[Precision][]string, error) {
	touched := map[Precision][]string{}
	for _, t := range Rollups {
		buckets, err := Buckets(ctx, q, provider, t.Precision, dates)
		if err != nil {
			return nil, fmt.Errorf("refresh %s: %w", t.Name, err)
		}
		if len(buckets) == 0 {
			continue
		}
		if err := rebuildBuckets(ctx, q, t, provider, buckets); err != nil {
			return nil, err
		}
		touched[t.Precision] = buckets
	}
	return touched, nil
}

// Buckets lists, sorted, the buckets at precision p of provider's daily rows
// dated on any of dates (Provider#buckets), as stored date text.
func Buckets(ctx context.Context, q db.Querier, provider string, p Precision, dates []time.Time) ([]string, error) {
	if len(dates) == 0 {
		return nil, nil
	}
	list := make([]string, len(dates))
	for i, d := range dates {
		list[i] = db.FormatDate(d)
	}
	rows, err := q.QueryContext(ctx, "SELECT DISTINCT "+BucketSQL(p, "date")+" FROM rates WHERE provider = ? AND "+
		"date IN "+db.LitList(list), provider)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var buckets []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		buckets = append(buckets, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(buckets)
	return buckets, nil
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
