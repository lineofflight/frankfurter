package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// retiredLabel is one of 041's or 043's stored-label repairs: provider's rows
// carrying from move to to, dated since onwards and before before when those
// are set and matching filter (a SQL condition) when that is, with every
// component divided by unit or multiplied by factor when either is set. An
// empty to deletes the rows. A conflicting row already stored under to fails
// the repair unless keepExisting is set, in which case it wins.
type retiredLabel struct {
	provider, from, to, since, before, filter string
	unit, factor                              float64
	keepExisting                              bool
}

// retiredLabels are the labels provider health could not place after a
// backfill retained older history (#735-#738).
var retiredLabels = []retiredLabel{
	{provider: "CBU", from: "SDR", to: "XDR"},                      // the pre-2008 label for the same unit
	{provider: "BOTA", from: "MXM", to: "MZM"},                     // old metical, July 1999
	{provider: "NBP", from: "AON", to: "AOA"},                      // retired label on AOA values, to 2003
	{provider: "NBP", from: "BYB", to: "BYR"},                      // retired label on BYR values, 2002
	{provider: "NBP", from: "AFA", to: "AFN", since: "2003-01-07"}, // new-afghani values under the old label
	{provider: "BOI", from: "BEL", to: "BEF", unit: 10},            // published per 10 francs
	{provider: "BOI", from: "ATS", to: "ATS", unit: 10},            // per 10 schillings
	{provider: "BOI", from: "ESP", to: "ESP", unit: 100},           // per 100 pesetas
	{provider: "BOI", from: "ITL", to: "ITL", unit: 1000},          // per 1000 lire
	{provider: "BOI", from: "CBK_L"},                               // the currency basket, not a currency
}

// retiredCodes are the legacy codes 041 retires, with the terminal dates of
// their new defunct entries, in Ruby's order.
var retiredCodes = []struct{ code, terminal string }{
	{"ADP", "2002-03-01"}, {"AFA", "2002-10-07"}, {"BGL", "1999-07-05"}, {"BYB", "2000-01-01"},
	{"MGF", "2005-01-01"}, {"MZM", "2006-07-01"}, {"SDD", "2007-01-10"}, {"SRG", "2004-01-01"},
	{"VEB", "2008-01-01"},
}

var retiredRollups = []struct {
	table, blended, bucket string
	precision              rates.Precision
}{
	{"weekly_rates", "blended_weekly_rates", weekBucket, rates.Week},
	{"monthly_rates", "blended_monthly_rates", monthBucket, rates.Month},
}

func (l retiredLabel) scope() string {
	s := "`provider` = " + db.Lit(l.provider) + " AND " + either(l.from)
	if l.since != "" {
		s += " AND `date` >= " + db.Lit(l.since)
	}
	if l.before != "" {
		s += " AND `date` < " + db.Lit(l.before)
	}
	if l.filter != "" {
		s += " AND " + l.filter
	}
	return s
}

// repairRetiredLabels is 041. The adapters now map the labels above; this
// repairs what is stored, since insert-only backfill never rewrites a row and
// the corrected rows have new keys. Equal duplicates collapse; a pair that
// disagrees fails the migration, since picking a winner needs source evidence.
//
// Rollups are rebuilt for every affected provider bucket and the grouped
// blends for those buckets are dropped. The same backfill brought legacy
// series quoted years past their redenominations at fixed multiples of the
// successors (BDI's BGL, MZM and VEB run to 2004, 2006 and 2012). The new
// defunct entries keep those observations out of blends and the catalogue, so
// their coverage is recomputed and grouped blends past each cutoff are dropped
// too. The daily blend is cleared for the scheduler to rebuild, and populate
// refills the grouped buckets.
func repairRetiredLabels(ctx context.Context, q db.Querier) error {
	var checks []string
	for _, l := range retiredLabels {
		p := db.Lit(l.provider)
		checks = append(checks,
			"EXISTS (SELECT 1 FROM `rates` WHERE "+l.scope()+")",
			"EXISTS (SELECT 1 FROM `currency_exclusions` WHERE `provider_key` = "+p+" AND `iso_code` = "+
				db.Lit(l.from)+")")
		for _, t := range retiredRollups {
			checks = append(checks, "EXISTS (SELECT 1 FROM `"+t.table+"` WHERE `provider` = "+p+" AND "+
				either(l.from)+")")
		}
	}
	var needed bool
	if err := q.QueryRowContext(ctx, "SELECT "+strings.Join(checks, " OR ")).Scan(&needed); err != nil {
		return err
	}
	// Coverages are read before the repair, as Ruby does.
	expired := make([][]string, len(retiredCodes))
	for i, r := range retiredCodes {
		providers, err := column(ctx, q, "SELECT `provider_key` FROM `currency_coverages` WHERE `iso_code` = "+
			db.Lit(r.code)+" AND `end_date` >= "+db.Lit(r.terminal)+" ORDER BY `provider_key`")
		if err != nil {
			return err
		}
		expired[i] = providers
		needed = needed || len(providers) > 0
	}
	if !needed {
		return nil
	}

	type bucketKey struct {
		provider string
		rollup   int // index into retiredRollups
	}
	var bucketKeys []bucketKey
	buckets := map[bucketKey][]string{}
	var providers []string
	codes := map[string][]string{}
	add := func(list []string, values ...string) []string {
		for _, v := range values {
			if !slices.Contains(list, v) {
				list = append(list, v)
			}
		}
		return list
	}
	for _, l := range retiredLabels {
		p := db.Lit(l.provider)
		for i, t := range retiredRollups {
			dates, err := column(ctx, q, "SELECT "+t.bucket+" FROM `rates` WHERE "+l.scope()+
				" UNION SELECT date(`bucket_date`) FROM `"+t.table+"` WHERE `provider` = "+p+" AND "+either(l.from))
			if err != nil {
				return err
			}
			k := bucketKey{l.provider, i}
			if _, ok := buckets[k]; !ok {
				bucketKeys = append(bucketKeys, k)
			}
			buckets[k] = add(buckets[k], dates...)
		}

		rows, err := legacyRows(ctx, q, l.scope())
		if err != nil {
			return err
		}
		if _, ok := codes[l.provider]; !ok {
			providers = append(providers, l.provider)
		}
		codes[l.provider] = add(codes[l.provider], l.from)
		if l.to != "" {
			codes[l.provider] = add(codes[l.provider], l.to)
		}
		for _, r := range rows {
			codes[l.provider] = add(codes[l.provider], r.base, r.quote)
		}

		if l.to == "" {
			if err := exec(ctx, q, "DELETE FROM `rates` WHERE "+l.scope()); err != nil {
				return err
			}
			continue
		}
		for _, r := range rows {
			if err := relabelRetiredRow(ctx, q, l, r); err != nil {
				return err
			}
		}
	}

	for _, k := range bucketKeys {
		dates := buckets[k]
		if len(dates) == 0 {
			continue
		}
		t := retiredRollups[k.rollup]
		p, list := db.Lit(k.provider), db.LitList(dates)
		if err := exec(ctx, q,
			"DELETE FROM `"+t.blended+"` WHERE `bucket_date` IN "+list,
			"DELETE FROM `"+t.table+"` WHERE `provider` = "+p+" AND `bucket_date` IN "+list,
			rollupInsert(t.table, t.bucket, "`provider` = "+p+" AND "+t.bucket+" IN "+list),
		); err != nil {
			return err
		}
	}
	for _, p := range providers {
		if err := rates.RefreshSummaries(ctx, q, codes[p], p); err != nil {
			return err
		}
	}

	for i, r := range retiredCodes {
		terminal, err := db.ParseDate(r.terminal)
		if err != nil {
			return err
		}
		lastValid := db.Lit(db.FormatDate(terminal.AddDate(0, 0, -1)))
		for _, provider := range expired[i] {
			p := db.Lit(provider)
			for _, t := range retiredRollups {
				// The bucket holding the last valid day straddles the cutoff,
				// so it changes too.
				var first string
				if err := q.QueryRowContext(ctx, "SELECT "+rates.BucketSQL(t.precision, lastValid)).
					Scan(&first); err != nil {
					return err
				}
				if err := exec(ctx, q, "DELETE FROM `"+t.blended+"` WHERE `bucket_date` IN (SELECT `bucket_date` FROM `"+
					t.table+"` WHERE `provider` = "+p+" AND "+either(r.code)+" AND `bucket_date` >= "+db.Lit(first)+
					")"); err != nil {
					return err
				}
			}
			if err := rates.RefreshSummaries(ctx, q, []string{r.code}, provider); err != nil {
				return err
			}
		}
	}

	// Daily readiness checks only the earliest date. Partial invalidation
	// could serve an incomplete table, so clear it entirely. The scheduler
	// rebuilds daily history and populates missing grouped buckets after
	// startup.
	return exec(ctx, q, "DELETE FROM `blended_rates`")
}

// successorLabels are 043's repairs: sources that keep a retired code after a
// redenomination and quote the successor under it, found by comparing each
// provider's values past the retirement against other providers' successor
// rates.
var successorLabels = []retiredLabel{
	{provider: "CBG", from: "SLL", to: "SLE", since: "2022-07-01"},
	{provider: "CBU", from: "TRL", to: "TRY", since: "2005-01-04"},
	{provider: "LB", from: "BYR", to: "BYB", before: "2000-01-01"}, // the 1994 ruble under its successor's code
	{provider: "NBU", from: "RUR", to: "RUB", since: "1998-01-01"},
	// Per 100 BGN from 2000, though the units field reads 1000.
	{provider: "NBU", from: "BGL", to: "BGN", since: "1999-08-01", before: "2000-01-01"},
	{provider: "NBU", from: "BGL", to: "BGN", since: "2000-01-01", factor: 10},
	// Per 100 of the successor to 2014, though the units field reads 10000.
	{provider: "NBU", from: "TRL", to: "TRY", since: "2005-01-06", factor: 100},
	{provider: "NBU", from: "ROL", to: "RON", since: "2005-07-01", factor: 100},
	{provider: "NBU", from: "AZM", to: "AZN", since: "2006-01-06", factor: 100},
	{provider: "NBU", from: "TMM", to: "TMT", since: "2009-01-06", factor: 100},
	{provider: "BNA", from: "MZM", to: "MZN", since: "2006-07-01"},
	{provider: "BNA", from: "STD", to: "STN", since: "2023-02-22"},
	// BNA's own VES row wins the one day it publishes both.
	{provider: "BNA", from: "VEF", to: "VES", since: "2023-10-18", keepExisting: true},
	{provider: "BDI", from: "ZWD", to: "ZWR", since: "2008-08-01", before: "2009-02-03"},
	{provider: "BDI", from: "ZWD", to: "ZWL", since: "2009-02-03"},
	{provider: "NBP", from: "ZWR", to: "ZWL", since: "2009-02-25"},
	// 53 rows quoted per 100 under a unit of 1. The new ouguiya trades near
	// 0.25 MAD, so a stored rate above 1 is a per-100 quote.
	{provider: "BAM", from: "MRO", to: "MRU", since: "2018-01-03", filter: "`rate` <= 1"},
	{provider: "BAM", from: "MRO", to: "MRU", since: "2018-01-03", filter: "`rate` > 1", factor: 0.01},
	{provider: "BOTA", from: "ZMK", to: "ZMW", since: "2013-01-01"},
	// A frozen 2016 rate the live feed re-dates every week.
	{provider: "NBKR", from: "BYR", since: "2016-07-01"},
}

// relabelSuccessorValues is 043. The adapters now emit the successor; this
// repairs what is stored, since insert-only backfill never rewrites a row.
// Equal duplicates collapse; a pair that disagrees fails the migration, since
// picking a winner needs source evidence, unless the source publishes the
// successor under its own code that day.
//
// Rollups are rebuilt for every affected provider bucket and coverage is
// recomputed. The blend tables are left as they are: the repaired history
// spans 1994 to today, so refreshing it is a full rebuild, too slow for a
// migration that runs before the app starts. Clearing them instead, as 041 and
// 042 did, sends every request to live compute until the scheduler rebuilds
// them. Run `frankfurter blend-rebuild` after deploy; it rebuilds in place and
// keeps the tables serving.
func relabelSuccessorValues(ctx context.Context, q db.Querier) error {
	return relabelStored(ctx, q, successorLabels)
}

// nbrmECULabels are 044's repairs. NBRM codes the ECU as XBA, the bond-market
// European Composite Unit, and keeps the label to May 1999, quoting the same
// value it publishes under EUR, so the rows from 1999 collapse into NBRM's own
// EUR rows.
var nbrmECULabels = []retiredLabel{
	{provider: "NBRM", from: "XBA", to: "XEU", before: "1999-01-01"},
	{provider: "NBRM", from: "XBA", to: "EUR", since: "1999-01-01"},
}

// relabelNBRMECU is 044. The adapter now emits XEU before the euro and EUR
// after; this repairs what is stored as 043 does, rebuilding NBRM's rollups
// for every affected bucket and recomputing coverage. The blend tables are
// left for `frankfurter blend-rebuild`.
func relabelNBRMECU(ctx context.Context, q db.Querier) error {
	return relabelStored(ctx, q, nbrmECULabels)
}

// relabelStored applies 043's and 044's repairs: each label's rows move to
// their corrected label and unit (or are deleted), the affected provider
// rollup buckets are rebuilt and the labels' codes resummarized. It does
// nothing when no label matches a stored row.
func relabelStored(ctx context.Context, q db.Querier, labels []retiredLabel) error {
	checks := make([]string, len(labels))
	for i, l := range labels {
		checks[i] = "EXISTS (SELECT 1 FROM `rates` WHERE " + l.scope() + ")"
	}
	var needed bool
	if err := q.QueryRowContext(ctx, "SELECT "+strings.Join(checks, " OR ")).Scan(&needed); err != nil {
		return err
	}
	if !needed {
		return nil
	}

	type bucketKey struct {
		provider string
		rollup   int // index into retiredRollups
	}
	var bucketKeys []bucketKey
	buckets := map[bucketKey][]string{}
	var providers []string
	codes := map[string][]string{}
	for _, l := range labels {
		for i, t := range retiredRollups {
			dates, err := column(ctx, q, "SELECT DISTINCT "+t.bucket+" FROM `rates` WHERE "+l.scope())
			if err != nil {
				return err
			}
			k := bucketKey{l.provider, i}
			if _, ok := buckets[k]; !ok {
				bucketKeys = append(bucketKeys, k)
			}
			for _, d := range dates {
				if !slices.Contains(buckets[k], d) {
					buckets[k] = append(buckets[k], d)
				}
			}
		}
		if _, ok := codes[l.provider]; !ok {
			providers = append(providers, l.provider)
		}
		for _, code := range []string{l.from, l.to} {
			if code != "" && !slices.Contains(codes[l.provider], code) {
				codes[l.provider] = append(codes[l.provider], code)
			}
		}

		if l.to == "" {
			if err := exec(ctx, q, "DELETE FROM `rates` WHERE "+l.scope()); err != nil {
				return err
			}
			continue
		}
		rows, err := legacyRows(ctx, q, l.scope())
		if err != nil {
			return err
		}
		for _, r := range rows {
			if err := relabelRetiredRow(ctx, q, l, r); err != nil {
				return err
			}
		}
	}

	for _, k := range bucketKeys {
		dates := buckets[k]
		if len(dates) == 0 {
			continue
		}
		t := retiredRollups[k.rollup]
		p, list := db.Lit(k.provider), db.LitList(dates)
		if err := exec(ctx, q,
			"DELETE FROM `"+t.table+"` WHERE `provider` = "+p+" AND `bucket_date` IN "+list,
			rollupInsert(t.table, t.bucket, "`provider` = "+p+" AND "+t.bucket+" IN "+list),
		); err != nil {
			return err
		}
	}
	for _, p := range providers {
		if err := rates.RefreshSummaries(ctx, q, codes[p], p); err != nil {
			return err
		}
	}
	return nil
}

// relabelRetiredRow moves one stored row to its corrected label and unit, or
// deletes it when the corrected row already exists with equal components (or
// with any, when the label keeps existing rows).
func relabelRetiredRow(ctx context.Context, q db.Querier, l retiredLabel, r storedRate) error {
	relabel := func(code string) string {
		if code == l.from {
			return l.to
		}
		return code
	}
	base, quote := relabel(r.base), relabel(r.quote)
	mid, bid, ask := r.mid, r.bid, r.ask
	if l.unit != 0 || l.factor != 0 {
		scale := func(v sql.NullFloat64) sql.NullFloat64 {
			if v.Valid {
				if l.unit != 0 {
					v.Float64 = rates.Normalize(v.Float64 / l.unit)
				} else {
					v.Float64 = rates.Normalize(v.Float64 * l.factor)
				}
			}
			return v
		}
		mid, bid, ask = scale(mid), scale(bid), scale(ask)
	}
	key := []any{r.provider, r.date, r.base, r.quote}

	if base != r.base || quote != r.quote {
		var existing storedRate
		err := q.QueryRowContext(ctx, "SELECT `mid`, `bid`, `ask` FROM `rates` WHERE `provider` = ? AND `date` = ? AND "+
			"`base` = ? AND `quote` = ?", r.provider, r.date, base, quote).Scan(&existing.mid, &existing.bid, &existing.ask)
		switch {
		case err == nil:
			if !l.keepExisting && (existing.mid != mid || existing.bid != bid || existing.ask != ask) {
				return fmt.Errorf("%s: conflicting %s/%s components on %s", l.provider, l.from, l.to, r.date)
			}
			_, err = q.ExecContext(ctx, "DELETE FROM `rates` WHERE `provider` = ? AND `date` = ? AND `base` = ? AND "+
				"`quote` = ?", key...)
			return err
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
	}
	_, err := q.ExecContext(ctx, "UPDATE `rates` SET `base` = ?, `quote` = ?, `mid` = ?, `bid` = ?, `ask` = ? WHERE "+
		"`provider` = ? AND `date` = ? AND `base` = ? AND `quote` = ?", append([]any{base, quote, mid, bid, ask}, key...)...)
	return err
}

// column returns the first column of every row query yields.
func column(ctx context.Context, q db.Querier, query string) ([]string, error) {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
