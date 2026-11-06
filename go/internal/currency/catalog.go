package currency

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/db"
)

// Currency is a row of the materialised currencies table, which backfill
// maintains incrementally (see rates.RefreshSummaries), widened by its peg when
// it has one.
type Currency struct {
	ISOCode   string
	StartDate time.Time
	EndDate   time.Time
	Peg       *Peg // nil unless pegged
}

// Name is the Money gem's name, or the code when the gem cannot name it.
func (c Currency) Name() string {
	if info, ok := Find(c.ISOCode); ok {
		return info.Name
	}
	return c.ISOCode
}

// Metadata is the Money gem's entry for the currency.
func (c Currency) Metadata() (Info, bool) { return Find(c.ISOCode) }

// ActiveDays is how recent a currency's end date must be for Active to list it.
const ActiveDays = 30

// All lists the catalogue with pegged currencies merged in, sorted by code.
func All(ctx context.Context, q db.Querier) ([]Currency, error) {
	rows, err := load(ctx, q, "SELECT iso_code, start_date, end_date FROM currencies")
	if err != nil {
		return nil, err
	}
	return mergePegged(rows), nil
}

// Active lists currencies whose end date lies within ActiveDays of today,
// pegged ones merged in.
func Active(ctx context.Context, q db.Querier, today time.Time) ([]Currency, error) {
	cutoff := today.AddDate(0, 0, -ActiveDays)
	rows, err := load(ctx, q, "SELECT iso_code, start_date, end_date FROM currencies WHERE end_date >= ?",
		db.FormatDate(cutoff))
	if err != nil {
		return nil, err
	}
	return mergePegged(rows), nil
}

// FindCurrency returns the currency with code, case-insensitively, or nil. A
// pegged currency no provider covers is derived from its anchor.
func FindCurrency(ctx context.Context, q db.Querier, code string) (*Currency, error) {
	code = strings.ToUpper(code)
	record, err := one(ctx, q, code)
	if err != nil {
		return nil, err
	}
	peg, pegged := FindPeg(code)
	if record != nil {
		if pegged {
			anchor, err := one(ctx, q, peg.Base)
			if err != nil {
				return nil, err
			}
			applyPeg(record, peg, anchor)
		}
		return record, nil
	}
	if !pegged {
		return nil, nil
	}
	anchor, err := one(ctx, q, peg.Base)
	if err != nil || anchor == nil {
		return nil, err
	}
	c := newPegged(peg, anchor)
	return &c, nil
}

// WithProviders lists the currencies the given providers cover, with dates
// merged across those providers only and no pegs, sorted by code.
func WithProviders(ctx context.Context, q db.Querier, keys []string) ([]Currency, error) {
	return load(ctx, q, `SELECT iso_code, min(start_date), max(end_date) FROM currency_coverages
		WHERE provider_key IN `+db.LitList(keys)+` GROUP BY iso_code ORDER BY iso_code`)
}

// Providers lists the keys of the providers covering code, sorted.
func Providers(ctx context.Context, q db.Querier, code string) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		"SELECT provider_key FROM currency_coverages WHERE iso_code = ? ORDER BY provider_key", code)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func one(ctx context.Context, q db.Querier, code string) (*Currency, error) {
	rows, err := load(ctx, q, "SELECT iso_code, start_date, end_date FROM currencies WHERE iso_code = ?", code)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

func load(ctx context.Context, q db.Querier, query string, args ...any) ([]Currency, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Currency
	for rows.Next() {
		var c Currency
		var start, end db.NullDate
		if err := rows.Scan(&c.ISOCode, &start, &end); err != nil {
			return nil, err
		}
		c.StartDate, c.EndDate = start.Time, end.Time
		out = append(out, c)
	}
	return out, rows.Err()
}

func mergePegged(stored []Currency) []Currency {
	byCode := make(map[string]int, len(stored))
	for i, c := range stored {
		byCode[c.ISOCode] = i
	}
	var pegged []Currency
	for _, peg := range Pegs() {
		var anchor *Currency
		if i, ok := byCode[peg.Base]; ok {
			anchor = &stored[i]
		}
		if i, ok := byCode[peg.Quote]; ok {
			applyPeg(&stored[i], peg, anchor)
			continue
		}
		if anchor != nil {
			pegged = append(pegged, newPegged(peg, anchor))
		}
	}
	all := append(stored, pegged...)
	sort.Slice(all, func(i, j int) bool { return all[i].ISOCode < all[j].ISOCode })
	return all
}

// applyPeg widens record to its anchor's range from the peg's start: a peg
// holds wherever the anchor has rates.
func applyPeg(record *Currency, peg Peg, anchor *Currency) {
	record.Peg = &peg
	if anchor == nil {
		return
	}
	start := peg.Since
	if anchor.StartDate.After(start) {
		start = anchor.StartDate
	}
	if start.Before(record.StartDate) {
		record.StartDate = start
	}
	if anchor.EndDate.After(record.EndDate) {
		record.EndDate = anchor.EndDate
	}
}

func newPegged(peg Peg, anchor *Currency) Currency {
	c := Currency{ISOCode: peg.Quote, StartDate: anchor.StartDate, EndDate: anchor.EndDate}
	applyPeg(&c, peg, anchor)
	return c
}
