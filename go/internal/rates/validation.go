package rates

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/db"
)

// MaxFutureDrift is how far ahead of today a fetched rate may be dated. Genuine forward value dates (far-eastern time
// zones, T+1 conventions) sit within a day or two; anything beyond is an upstream typo or a stray row. Storing it would
// hijack last_synced (the max date) and freeze backfill behind an unreachable cursor. An adapter that publishes ahead
// of its period (HMRC: next month's customs rates, this month) declares a lead, which extends the horizon by that much.
const MaxFutureDrift = 2

// Horizon is the latest date a row may carry for an adapter with the given lead.
func Horizon(today time.Time, leadDays int) time.Time {
	return today.AddDate(0, 0, MaxFutureDrift+leadDays)
}

// Reject is RateValidation.reject!: it keeps provider-published rows, dropping non-positive (or NaN) rates and dates
// beyond the horizon, and relabels a code used before its currency existed with the known predecessor, dropping the
// row when there is none. Currency eligibility is applied when blending, not here. It filters records in place and
// returns the kept prefix.
func Reject(records []adapter.Rate, leadDays int, today time.Time) []adapter.Rate {
	return reject(records, leadDays, today, currency.FindNascent)
}

func reject(records []adapter.Rate, leadDays int, today time.Time, nascent func(string) (currency.Nascent, bool)) []adapter.Rate {
	horizon := Horizon(today, leadDays)
	kept := records[:0]
	for _, r := range records {
		if !(r.Rate > 0) || r.Date.After(horizon) {
			continue
		}
		ok := true
		for _, side := range []*string{&r.Base, &r.Quote} {
			entry, found := nascent(*side)
			if !found || !r.Date.Before(entry.InceptionDate) {
				continue
			}
			if entry.Predecessor == "" {
				ok = false
				break
			}
			*side = entry.Predecessor
		}
		if ok {
			kept = append(kept, r)
		}
	}
	return kept
}

// ProviderLeads maps each seeded provider whose registered adapter declares a lead to that lead. Providers without a
// registered adapter (test fixtures, or adapters the binary does not import) have none.
func ProviderLeads(ctx context.Context, q db.Querier) (map[string]int, error) {
	rows, err := q.QueryContext(ctx, "SELECT key FROM providers")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	leads := map[string]int{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		if build, ok := adapter.Lookup(key); ok {
			if lead := build(&http.Client{}).LeadDays(); lead != 0 {
				leads[key] = lead
			}
		}
	}
	return leads, rows.Err()
}

// futureScope is FutureDate.reject_scope: rows of t beyond the horizon, per provider lead. Rollup buckets anchor to a
// fixed weekday or the first of the month, so the live period's bucket can sit a few days ahead of the latest date it
// summarises; the horizon is bucketed to the table's precision so only buckets wholly beyond it go.
func futureScope(t Table, today time.Time, leads map[string]int) Query {
	col := t.DateColumn
	bound := func(lead int) string { return BucketSQL(t.Precision, db.LitDate(Horizon(today, lead))) }
	cond := "(" + col + " > " + bound(0) + ")"
	if len(leads) > 0 {
		keys := make([]string, 0, len(leads))
		for k := range leads {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		cond = "(" + cond + " AND (provider NOT IN " + db.LitList(keys) + "))"
		for _, k := range keys {
			cond += " OR ((provider = " + db.Lit(k) + ") AND (" + col + " > " + bound(leads[k]) + "))"
		}
	}
	return t.Dataset().Filter(cond)
}

// PurgeTotals counts the rows Purge rejected per table. Replacing a provider bucket does not add to them.
type PurgeTotals struct {
	Rates, Weekly, Monthly int
}

// Total is the sum over tables.
func (p PurgeTotals) Total() int { return p.Rates + p.Weekly + p.Monthly }

// Purge is RateValidation.purge: it deletes stored rows beyond each provider's horizon from every rate table, rebuilds
// the provider rollup buckets they touched from the surviving dailies (clearing those buckets' blends for blending
// providers), and refreshes the currency summaries of every code involved, all in one immediate transaction. leads
// comes from ProviderLeads.
func Purge(ctx context.Context, conn *sql.DB, today time.Time, leads map[string]int) (PurgeTotals, error) {
	var totals PurgeTotals
	err := db.Immediate(ctx, conn, func(q db.Querier) error {
		var err error
		totals, err = purge(ctx, q, today, leads)
		return err
	})
	return totals, err
}

func purge(ctx context.Context, q db.Querier, today time.Time, leads map[string]int) (PurgeTotals, error) {
	var totals PurgeTotals
	var affected []string
	repairs := map[Precision]map[string][]string{Week: {}, Month: {}}

	for _, t := range Tables {
		scope := futureScope(t, today, leads)
		codes, err := pairCodes(ctx, q, scope.Columns("base, quote").SQL())
		if err != nil {
			return totals, err
		}
		affected = append(affected, codes...)

		if t.Precision != Day {
			if err := collect(ctx, q, scope.Columns("DISTINCT provider, bucket_date").SQL(), repairs[t.Precision]); err != nil {
				return totals, err
			}
		} else {
			for _, r := range Rollups {
				sel := "DISTINCT provider, " + BucketSQL(r.Precision, "date") + " AS bucket_date"
				if err := collect(ctx, q, scope.Columns(sel).SQL(), repairs[r.Precision]); err != nil {
					return totals, err
				}
			}
		}

		res, err := q.ExecContext(ctx, "DELETE FROM "+t.Name+" WHERE "+scope.Condition())
		if err != nil {
			return totals, fmt.Errorf("purge %s: %w", t.Name, err)
		}
		n, _ := res.RowsAffected()
		switch t {
		case Daily:
			totals.Rates += int(n)
		case Weekly:
			totals.Weekly += int(n)
		case Monthly:
			totals.Monthly += int(n)
		}
	}

	nonBlending, err := NonBlendingKeys(ctx, q)
	if err != nil {
		return totals, err
	}
	for _, t := range Rollups {
		providers := repairs[t.Precision]
		keys := make([]string, 0, len(providers))
		for k := range providers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, provider := range keys {
			// Captured buckets drive deletion even when no daily rows survive; retained periods are rebuilt from the
			// remaining observations.
			if err := rebuildBuckets(ctx, q, t, provider, providers[provider]); err != nil {
				return totals, err
			}
			if !contains(nonBlending, provider) {
				if _, err := q.ExecContext(ctx, "DELETE FROM blended_"+t.Name+" WHERE bucket_date IN "+
					db.LitList(providers[provider])); err != nil {
					return totals, err
				}
			}
		}
	}

	if len(affected) > 0 {
		if err := RefreshSummaries(ctx, q, uniq(affected), ""); err != nil {
			return totals, err
		}
	}
	return totals, nil
}

// rebuildBuckets replaces provider's buckets in rollup t with averages of its daily rows.
func rebuildBuckets(ctx context.Context, q db.Querier, t Table, provider string, buckets []string) error {
	list := db.LitList(buckets)
	if _, err := q.ExecContext(ctx, "DELETE FROM "+t.Name+" WHERE provider = ? AND bucket_date IN "+list,
		provider); err != nil {
		return fmt.Errorf("rebuild %s: %w", t.Name, err)
	}
	bucket := BucketSQL(t.Precision, "date")
	_, err := q.ExecContext(ctx, "INSERT INTO "+t.Name+" (bucket_date, provider, base, quote, rate) SELECT "+bucket+
		", provider, base, quote, avg(rate) FROM rates WHERE provider = ? AND "+bucket+" IN "+list+
		" GROUP BY provider, base, quote, "+bucket, provider)
	if err != nil {
		return fmt.Errorf("rebuild %s: %w", t.Name, err)
	}
	return nil
}

// collect adds each (provider, bucket_date) row of query to into, once per bucket.
func collect(ctx context.Context, q db.Querier, query string, into map[string][]string) error {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var provider string
		var bucket db.NullDate
		if err := rows.Scan(&provider, &bucket); err != nil {
			return err
		}
		d := db.FormatDate(bucket.Time)
		if !contains(into[provider], d) {
			into[provider] = append(into[provider], d)
		}
	}
	return rows.Err()
}

// pairCodes returns the base and quote of every row of query, flattened.
func pairCodes(ctx context.Context, q db.Querier, query string) ([]string, error) {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			return nil, err
		}
		out = append(out, a, b)
	}
	return out, rows.Err()
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func uniq(list []string) []string {
	seen := make(map[string]bool, len(list))
	out := list[:0:0]
	for _, v := range list {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
