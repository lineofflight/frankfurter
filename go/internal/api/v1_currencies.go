package api

import (
	"context"
	"database/sql"
	"sort"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// v1CurrencyNames is Versions::V1::CurrencyNames: the euro plus every currency ECB quoted in the last 14 days, by name.
// It reads blendable rows, so unnamed and expired codes stay out of the legacy catalogue.
type v1CurrencyNames struct {
	rows []rates.Row
}

func loadV1CurrencyNames(ctx context.Context, conn *sql.DB, today time.Time) (*v1CurrencyNames, error) {
	nonBlending, err := rates.NonBlendingKeys(ctx, conn)
	if err != nil {
		return nil, err
	}
	query := rates.Daily.Blendable(nonBlending).
		Filter("provider = 'ECB'").
		Filter("date >= " + db.LitDate(today.AddDate(0, 0, -rates.LookbackDays)) + " AND date <= " + db.LitDate(today)).
		Columns("date, base, quote, provider, rate").
		OrderBy("date, base, quote")
	rows, err := rates.Select(ctx, conn, query.SQL())
	if err != nil {
		return nil, err
	}
	return &v1CurrencyNames{rows: rates.CarryForward(rows, today, rates.LookbackDays)}, nil
}

// CacheKey hashes the first snapshot row's date; empty when there are none.
func (c *v1CurrencyNames) CacheKey() string {
	if len(c.rows) == 0 {
		return ""
	}
	return md5Hex(db.FormatDate(c.rows[0].Date))
}

// Formatted maps each code to its name; empty when ECB has nothing recent.
func (c *v1CurrencyNames) Formatted() map[string]string {
	out := map[string]string{}
	if len(c.rows) == 0 {
		return out
	}
	codes := []string{"EUR"}
	for _, r := range c.rows {
		codes = append(codes, r.Quote)
	}
	sort.Strings(codes)
	for _, code := range codes {
		info, _ := currency.Find(code)
		out[code] = info.Name
	}
	return out
}
