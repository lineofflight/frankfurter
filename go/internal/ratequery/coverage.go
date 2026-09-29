package ratequery

import (
	"context"
	"database/sql"
	"slices"
	"strconv"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// Coverage is a query's queryable history (RateCoverage): the first and last stored observation dates at which a
// snapshot returns at least one requested record. The bounds do not promise an uninterrupted series, extend history by
// carry-forward days, or confuse a carried quote's date with the arrival of its base bridge.
type Coverage struct {
	Base      string   `json:"base"`
	Quotes    []string `json:"quotes"`
	Providers []string `json:"providers"`
	StartDate *string  `json:"start_date"`
	EndDate   *string  `json:"end_date"`
}

// CoveragePageSize is how many candidate dates a boundary search reads at a time.
const CoveragePageSize = 64

// History computes the query's coverage. Live coverage holds a heavy slot while it runs.
func (q *Query) History(ctx context.Context) (Coverage, error) {
	out := Coverage{Base: q.base, Quotes: q.quotes, Providers: q.providers}
	stored, err := q.blendedTable(ctx)
	if err != nil {
		return out, err
	}
	if !stored {
		if err := q.acquireSlot(); err != nil {
			return out, err
		}
		defer q.ReleaseSlot()
	}
	dates, err := q.coverageDates(ctx, stored)
	if err != nil {
		return out, err
	}
	first, err := q.coverageBoundary(ctx, dates, stored, false)
	if err != nil || first == nil {
		return out, err
	}
	last, err := q.coverageBoundary(ctx, dates.Filter("date >= "+db.LitDate(*first)), stored, true)
	if err != nil {
		return out, err
	}
	start := db.FormatDate(*first)
	out.StartDate = &start
	if last != nil {
		end := db.FormatDate(*last)
		out.EndDate = &end
	}
	return out, nil
}

// coverageDates lists the candidate anchors: the stored dates of the query's raw rows (so live and stored bounds agree
// when a publication yields only carried-forward output), pruned by EXISTS checks that only rule out impossible
// snapshots. Merely overlapping currency date bounds say nothing about gaps, USD connectivity, or a single provider's
// native cross; the snapshot decides.
func (q *Query) coverageDates(ctx context.Context, stored bool) (rates.Query, error) {
	raw, err := q.rawScope(ctx, rates.Daily)
	if err != nil {
		return rates.Query{}, err
	}
	dates := rates.Query{Table: rates.Daily, From: "rates AS coverage_dates", Where: raw.Where}.Columns("DISTINCT date")
	if q.providerPegBase() {
		return dates.Filter("0"), nil
	}
	lookback, err := q.lookbackDays(ctx)
	if err != nil {
		return rates.Query{}, err
	}
	presence := func(ds rates.Query, alias string) error {
		cond, err := q.coveragePresence(ctx, ds, alias, lookback)
		if err == nil {
			dates = dates.Filter(cond)
		}
		return err
	}

	if stored {
		table := rates.Query{Table: rates.Daily, From: "blended_rates"}
		if q.base == Pivot {
			err = presence(table, "blended_rates")
		} else {
			err = presence(table.Filter("quote = "+db.Lit(q.base)), "blended_rates")
		}
		if err != nil {
			return rates.Query{}, err
		}
		if q.quotes != nil && !slices.Contains(q.quotes, q.base) && !slices.Contains(q.quotes, Pivot) {
			if err := presence(table.Filter("quote IN "+db.LitList(q.quotes)), "blended_rates"); err != nil {
				return rates.Query{}, err
			}
		}
		return dates, nil
	}

	_, basePegged := currency.FindPeg(q.base)
	if q.providers != nil || !basePegged {
		if err := presence(currencyRows(raw, []string{q.base}), "rates"); err != nil {
			return rates.Query{}, err
		}
	}
	if q.quotes != nil {
		anyPegged := slices.ContainsFunc(q.quotes, func(code string) bool { _, ok := currency.FindPeg(code); return ok })
		if q.providers != nil || !anyPegged {
			if err := presence(currencyRows(raw, q.quotes), "rates"); err != nil {
				return rates.Query{}, err
			}
		}
	}
	return dates, nil
}

func currencyRows(ds rates.Query, codes []string) rates.Query {
	list := db.LitList(codes)
	return ds.Filter("(base IN " + list + ") OR (quote IN " + list + ")")
}

// coveragePresence is the condition that an anchor date lies within ds's stored range (extended by the lookback) and
// has a ds row within the lookback before it. A ds with no rows rules out every anchor.
func (q *Query) coveragePresence(ctx context.Context, ds rates.Query, alias string, lookback int) (string, error) {
	if err := q.checkDeadline(); err != nil {
		return "", err
	}
	var first sql.NullString
	if err := q.db.QueryRowContext(ctx, ds.Columns("min(date)").SQL()).Scan(&first); err != nil {
		return "", err
	}
	if !first.Valid {
		return "0", nil
	}
	if err := q.checkDeadline(); err != nil {
		return "", err
	}
	var last sql.NullString
	if err := q.db.QueryRowContext(ctx, ds.Columns("max(date)").SQL()).Scan(&last); err != nil {
		return "", err
	}
	if err := q.checkDeadline(); err != nil {
		return "", err
	}
	n := strconv.Itoa(lookback)
	anchor := "coverage_dates.date"
	exists := ds.Columns("*").
		Filter(alias + ".date <= " + anchor).
		Filter(alias + ".date >= date(" + anchor + ", " + db.Lit("-"+n+" days") + ")")
	return anchor + " >= " + db.Lit(first.String) + " AND " + anchor + " <= date(" + db.Lit(last.String) + ", " +
		db.Lit("+"+n+" days") + ") AND EXISTS (" + exists.SQL() + ")", nil
}

// coverageBoundary searches dates from one end in keyset pages, so memory stays bounded and a century of interior
// history is never computed just to describe its extent; each snapshot shares the rate deadline.
func (q *Query) coverageBoundary(ctx context.Context, dates rates.Query, stored, reverse bool) (*time.Time, error) {
	order := "date"
	if reverse {
		order = "date DESC"
	}
	for {
		if err := q.checkDeadline(); err != nil {
			return nil, err
		}
		page, err := q.datePage(ctx, dates.OrderBy(order).SQL()+" LIMIT "+strconv.Itoa(CoveragePageSize))
		if err != nil {
			return nil, err
		}
		for _, date := range page {
			if err := q.checkDeadline(); err != nil {
				return nil, err
			}
			found := false
			if err := q.eachSnapshot(ctx, date, stored, func(Record) error { found = true; return nil }); err != nil {
				return nil, err
			}
			if found {
				return &date, nil
			}
		}
		if len(page) < CoveragePageSize {
			return nil, nil
		}
		cursor := db.LitDate(page[len(page)-1])
		if reverse {
			dates = dates.Filter("date < " + cursor)
		} else {
			dates = dates.Filter("date > " + cursor)
		}
	}
}

func (q *Query) datePage(ctx context.Context, query string) ([]time.Time, error) {
	rows, err := q.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var d db.NullDate
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d.Time)
	}
	return out, rows.Err()
}
