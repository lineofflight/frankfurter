package rates

import (
	"context"
	"fmt"

	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/db"
)

// RefreshSummaries is CurrencySummary.refresh: it recomputes, per code, the
// provider coverage ranges and the catalogue row from stored daily rates.
// Named, current observations define coverage; codes the Money gem cannot name
// keep separate publication ranges in currency_exclusions, so provider health
// can report them without rescanning history. The catalogue row spans the
// coverage of blending providers only. A non-empty provider limits the refresh
// of coverage and exclusions to that provider's rows.
func RefreshSummaries(ctx context.Context, q db.Querier, codes []string, provider string) error {
	nonBlending, err := NonBlendingKeys(ctx, q)
	if err != nil {
		return err
	}
	for _, code := range uniq(codes) {
		if err := refreshSummary(ctx, q, code, provider, nonBlending); err != nil {
			return fmt.Errorf("refresh summary %s: %w", code, err)
		}
	}
	return nil
}

func refreshSummary(ctx context.Context, q db.Querier, code, provider string, nonBlending []string) error {
	lit := db.Lit(code)
	rows := "((base = " + lit + ") OR (quote = " + lit + "))"
	scope := "iso_code = " + lit
	if provider != "" {
		rows += " AND (provider = " + db.Lit(provider) + ")"
		scope += " AND provider_key = " + db.Lit(provider)
	}
	for _, table := range []string{"currency_coverages", "currency_exclusions"} {
		if _, err := q.ExecContext(ctx, "DELETE FROM "+table+" WHERE "+scope); err != nil {
			return err
		}
	}

	named := currency.Named(code)
	target := "currency_exclusions"
	if named {
		rows += " AND (" + NamedCondition() + ") AND (" + CurrentCondition("date", Day) + ")"
		target = "currency_coverages"
	}
	if _, err := q.ExecContext(ctx, "INSERT INTO "+target+
		" (provider_key, iso_code, start_date, end_date) SELECT provider, "+lit+
		", min(date), max(date) FROM rates WHERE "+rows+" GROUP BY provider"); err != nil {
		return err
	}

	if _, err := q.ExecContext(ctx, "DELETE FROM currencies WHERE iso_code = ?", code); err != nil {
		return err
	}
	if !named {
		return nil
	}
	coverage := "iso_code = " + lit
	if len(nonBlending) > 0 {
		coverage += " AND provider_key NOT IN " + db.LitList(nonBlending)
	}
	_, err := q.ExecContext(ctx, "INSERT INTO currencies (iso_code, start_date, end_date) SELECT "+lit+
		", min(start_date), max(end_date) FROM currency_coverages WHERE "+coverage+" HAVING min(start_date) IS NOT NULL")
	return err
}
