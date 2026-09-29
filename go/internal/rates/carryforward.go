package rates

import (
	"context"
	"database/sql"
	"math"
	"sort"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/db"
)

// Row is a stored observation as read back: Quote units per one Base on Date
// from Provider.
type Row struct {
	Date     time.Time
	Base     string
	Quote    string
	Provider string
	Rate     float64 // NaN when the stored row resolves no rate (a single published side), Ruby's nil
}

// Select runs query, which must yield date, base, quote, provider and rate in
// that order, and scans the rows.
func Select(ctx context.Context, q db.Querier, query string, args ...any) ([]Row, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		var date db.NullDate
		var rate sql.NullFloat64
		if err := rows.Scan(&date, &r.Base, &r.Quote, &r.Provider, &rate); err != nil {
			return nil, err
		}
		r.Date, r.Rate = date.Time, rate.Float64
		if !rate.Valid {
			r.Rate = math.NaN()
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LookbackDays is the default carry-forward window.
const LookbackDays = 14

type seriesKey struct{ provider, base, quote string }

func keyOf(r Row) seriesKey { return seriesKey{r.Provider, r.Base, r.Quote} }

// CarryForward is CarryForward.apply: the snapshot of rows as of date, keeping
// each provider and pair's most recent row dated within lookback days on or
// before date. Snapshots power latest and single-date queries and each anchor
// of a range. Rows come back in order of each series' first eligible
// appearance.
func CarryForward(rows []Row, date time.Time, lookback int) []Row {
	cutoff := date.AddDate(0, 0, -lookback)
	index := map[seriesKey]int{}
	var best []Row
	for _, r := range rows {
		if r.Date.Before(cutoff) || r.Date.After(date) {
			continue
		}
		k := keyOf(r)
		i, ok := index[k]
		if !ok {
			index[k] = len(best)
			best = append(best, r)
		} else if r.Date.After(best[i].Date) {
			best[i] = r
		}
	}
	return best
}

// EachSnapshot is CarryForward.each_snapshot: CarryForward at every date in
// dates, ascending, sharing one pass over rows. Each series keeps a cursor that
// only moves forward as the anchors increase.
func EachSnapshot(rows []Row, dates []time.Time, lookback int, yield func(date time.Time, contributors []Row)) {
	var order []seriesKey
	groups := map[seriesKey][]Row{}
	for _, r := range rows {
		k := keyOf(r)
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], r)
	}
	for _, g := range groups {
		sort.SliceStable(g, func(i, j int) bool { return g[i].Date.Before(g[j].Date) })
	}
	cursors := make(map[seriesKey]int, len(groups))
	for _, k := range order {
		cursors[k] = -1
	}

	sorted := append([]time.Time(nil), dates...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Before(sorted[j]) })
	for _, date := range sorted {
		cutoff := date.AddDate(0, 0, -lookback)
		var contributors []Row
		for _, k := range order {
			g, i := groups[k], cursors[k]
			for i+1 < len(g) && !g[i+1].Date.After(date) {
				i++
			}
			cursors[k] = i
			if i >= 0 && !g[i].Date.Before(cutoff) {
				contributors = append(contributors, g[i])
			}
		}
		yield(date, contributors)
	}
}
