package rates

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/db"
)

// A spike is a provider's one-day typo (RateSpike): an observation at least
// SpikeFactor times off both its previous and next observation of the same
// pair, in the same direction, while those two agree within SpikeFactor of
// each other. A move that persists (a devaluation, a redenomination) never
// qualifies, since the next observation stays with it. rates keeps the
// published value and provider queries serve it; Blendable leaves it out of
// blends, so the provider's previous observation carries forward in its place.
//
// Judging needs the next observation, so the latest one blends until its
// successor arrives and backfill rescreens it. Neighbours more than
// SpikeMaxGapDays away are too far apart to tell a typo from a move, so a
// sparse series is never screened, and an insert can only change the screening
// of observations within SpikeMaxGapDays of the inserted dates.
const (
	SpikeFactor     = 3
	SpikeMaxGapDays = LookbackDays
)

// DetectSpikes is RateSpike.detect: the spikes among rows (a scope over
// rates), as provider, date, base, quote. Neighbours come from the same rows,
// so the scope must reach SpikeMaxGapDays past every observation to be judged.
// The result filters like any Query.
func DetectSpikes(rows Query) Query {
	window := " OVER (PARTITION BY provider, base, quote ORDER BY date)"
	neighbours := rows.Columns("provider, date, base, quote, rate, lag(rate)" + window + " AS prev_rate, lead(rate)" +
		window + " AS next_rate, lag(date)" + window + " AS prev_date, lead(date)" + window + " AS next_date").
		OrderBy("")

	// SQLite's two-argument max and min are scalar and return NULL when
	// either neighbour is missing.
	high, low := "max(prev_rate, next_rate)", "min(prev_rate, next_rate)"
	gap, factor := strconv.Itoa(SpikeMaxGapDays), strconv.Itoa(SpikeFactor)
	return Query{Table: Daily, From: "(" + neighbours.SQL() + ") AS t1", Select: "provider, date, base, quote"}.
		Filter("(julianday(date) - julianday(prev_date)) <= " + gap).
		Filter("(julianday(next_date) - julianday(date)) <= " + gap).
		Filter(high + " < (" + low + " * " + factor + ")").
		Filter("(rate >= (" + high + " * " + factor + ")) OR ((rate * " + factor + ") <= " + low + ")")
}

// RefreshSpikes is RateSpike.refresh: it rescreens provider's observations
// around newly inserted dates and returns the dates whose screening changed,
// sorted, whose blends the caller must refresh. Typically that is the previous
// observation, now that its successor has arrived.
func RefreshSpikes(ctx context.Context, q db.Querier, provider string, dates []time.Time) ([]time.Time, error) {
	if len(dates) == 0 {
		return nil, nil
	}
	first, last := slices.MinFunc(dates, time.Time.Compare), slices.MaxFunc(dates, time.Time.Compare)
	from, to := first.AddDate(0, 0, -SpikeMaxGapDays), last.AddDate(0, 0, SpikeMaxGapDays)
	judged := "(date >= " + db.LitDate(from) + ") AND (date <= " + db.LitDate(to) + ")"
	scope := Daily.Dataset().Filter("provider = " + db.Lit(provider)).
		Filter("date >= " + db.LitDate(from.AddDate(0, 0, -SpikeMaxGapDays))).
		Filter("date <= " + db.LitDate(to.AddDate(0, 0, SpikeMaxGapDays)))

	detected := DetectSpikes(scope).Filter(judged)
	found, err := spikeKeys(ctx, q, detected.Columns("date, base, quote").SQL())
	if err != nil {
		return nil, fmt.Errorf("detect spikes: %w", err)
	}
	stored := "FROM rate_spikes WHERE (provider = " + db.Lit(provider) + ") AND " + judged
	flagged, err := spikeKeys(ctx, q, "SELECT date, base, quote "+stored)
	if err != nil {
		return nil, fmt.Errorf("read spikes: %w", err)
	}

	var changed []time.Time
	note := func(keys, others []spikeKey) {
		for _, k := range keys {
			if !slices.Contains(others, k) && !slices.ContainsFunc(changed, k.date.Equal) {
				changed = append(changed, k.date)
			}
		}
	}
	note(found, flagged)
	note(flagged, found)
	if len(changed) == 0 {
		return nil, nil
	}
	slices.SortFunc(changed, time.Time.Compare)

	if _, err := q.ExecContext(ctx, "DELETE "+stored); err != nil {
		return nil, fmt.Errorf("clear spikes: %w", err)
	}
	if _, err := q.ExecContext(ctx, "INSERT INTO rate_spikes (provider, date, base, quote) "+detected.SQL()); err != nil {
		return nil, fmt.Errorf("flag spikes: %w", err)
	}
	return changed, nil
}

type spikeKey struct {
	date        time.Time
	base, quote string
}

func spikeKeys(ctx context.Context, q db.Querier, query string) ([]spikeKey, error) {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []spikeKey
	for rows.Next() {
		var k spikeKey
		var date db.NullDate
		if err := rows.Scan(&date, &k.base, &k.quote); err != nil {
			return nil, err
		}
		k.date = date.Time
		out = append(out, k)
	}
	return out, rows.Err()
}
