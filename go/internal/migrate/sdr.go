package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// normalizeSDR is 036 and 038-040: provider's adapter now emits XDR for the SDR
// it used to label SDR, and this relabels what is stored. home is the
// provider's own currency, whose summary also changes.
//
// Equal duplicates collapse; a pair that disagrees on any published component
// fails the migration, since picking a winner needs source evidence. Rollups
// are rebuilt from the merged daily history, and the grouped blends of every
// touched bucket (old buckets that disappear included) are dropped for the
// scheduler's populate job to refill. The daily blend is cleared outright: its
// readiness checks only the earliest date, so a partial invalidation could
// serve an incomplete table.
func normalizeSDR(provider, home string) func(context.Context, db.Querier) error {
	return func(ctx context.Context, q db.Querier) error {
		p := db.Lit(provider)
		oldCode := either("SDR")
		var needed bool
		err := q.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM `rates` WHERE `provider` = "+p+" AND "+oldCode+
			") OR EXISTS (SELECT 1 FROM `weekly_rates` WHERE `provider` = "+p+" AND "+oldCode+
			") OR EXISTS (SELECT 1 FROM `monthly_rates` WHERE `provider` = "+p+" AND "+oldCode+
			") OR EXISTS (SELECT 1 FROM `currency_exclusions` WHERE `provider_key` = "+p+" AND `iso_code` = 'SDR')",
		).Scan(&needed)
		if err != nil || !needed {
			return err
		}

		legacy, err := legacyRows(ctx, q, "`provider` = "+p+" AND "+oldCode)
		if err != nil {
			return err
		}
		codes := []string{"SDR", "XDR"}
		for _, r := range legacy {
			for _, c := range []string{r.base, r.quote} {
				if !slices.Contains(codes, c) {
					codes = append(codes, c)
				}
			}
			if err := normalizeRow(ctx, q, r); err != nil {
				return err
			}
		}
		if !slices.Contains(codes, home) {
			codes = append(codes, home)
		}

		pairs := "`provider` = " + p + " AND " + either("SDR", "XDR")
		for _, t := range []struct{ table, blended, bucket string }{
			{"weekly_rates", "blended_weekly_rates", weekBucket},
			{"monthly_rates", "blended_monthly_rates", monthBucket},
		} {
			if err := exec(ctx, q,
				"DELETE FROM `"+t.blended+"` WHERE `bucket_date` IN (SELECT `bucket_date` FROM `"+t.table+"` WHERE "+
					pairs+" UNION SELECT "+t.bucket+" FROM `rates` WHERE "+pairs+")",
				"DELETE FROM `"+t.table+"` WHERE "+pairs,
				rollupInsert(t.table, t.bucket, pairs),
			); err != nil {
				return err
			}
		}

		if err := rates.RefreshSummaries(ctx, q, codes, provider); err != nil {
			return err
		}
		return exec(ctx, q, "DELETE FROM `blended_rates`")
	}
}

type storedRate struct {
	provider, date, base, quote string
	mid, bid, ask               sql.NullFloat64
}

func legacyRows(ctx context.Context, q db.Querier, where string) ([]storedRate, error) {
	rows, err := q.QueryContext(ctx, "SELECT `provider`, `date`, `base`, `quote`, `mid`, `bid`, `ask` FROM `rates` WHERE "+
		where+" ORDER BY `date`, `base`, `quote`")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storedRate
	for rows.Next() {
		var r storedRate
		var date db.NullDate
		if err := rows.Scan(&r.provider, &date, &r.base, &r.quote, &r.mid, &r.bid, &r.ask); err != nil {
			return nil, err
		}
		r.date = db.FormatDate(date.Time)
		out = append(out, r)
	}
	return out, rows.Err()
}

func sdrToXDR(code string) string {
	if code == "SDR" {
		return "XDR"
	}
	return code
}

// normalizeRow moves one SDR row to XDR, or deletes it when an XDR row with the
// same components already exists.
func normalizeRow(ctx context.Context, q db.Querier, r storedRate) error {
	base, quote := sdrToXDR(r.base), sdrToXDR(r.quote)
	var existing storedRate
	err := q.QueryRowContext(ctx, "SELECT `mid`, `bid`, `ask` FROM `rates` WHERE `provider` = ? AND `date` = ? AND "+
		"`base` = ? AND `quote` = ?", r.provider, r.date, base, quote).Scan(&existing.mid, &existing.bid, &existing.ask)
	key := []any{r.provider, r.date, r.base, r.quote}
	switch {
	case err == sql.ErrNoRows:
		_, err = q.ExecContext(ctx, "UPDATE `rates` SET `base` = ?, `quote` = ? WHERE `provider` = ? AND `date` = ? AND "+
			"`base` = ? AND `quote` = ?", append([]any{base, quote}, key...)...)
		return err
	case err != nil:
		return err
	}
	if existing.mid != r.mid || existing.bid != r.bid || existing.ask != r.ask {
		return fmt.Errorf("%s: conflicting SDR/XDR components on %s (%s/%s)", r.provider, r.date, r.base, r.quote)
	}
	_, err = q.ExecContext(ctx, "DELETE FROM `rates` WHERE `provider` = ? AND `date` = ? AND `base` = ? AND `quote` = ?",
		key...)
	return err
}

// recognizeComesaDollar is 037. CMD was retained from RBM as an unknown code;
// registering the COMESA Dollar makes its observations eligible for the
// catalogue and blends. The daily blend needs a full rebuild, so it is left
// empty for the scheduler; grouped blends lose only the buckets whose RBM
// source includes CMD, for the populate job to repair.
func recognizeComesaDollar(ctx context.Context, q db.Querier) error {
	cmd := "`provider` = 'RBM' AND " + either("CMD")
	var found bool
	if err := q.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM `rates` WHERE "+cmd+")").Scan(&found); err != nil ||
		!found {
		return err
	}
	if err := rates.RefreshSummaries(ctx, q, []string{"CMD", "MWK"}, ""); err != nil {
		return err
	}
	return exec(ctx, q,
		"DELETE FROM `blended_rates`",
		"DELETE FROM `blended_weekly_rates` WHERE `bucket_date` IN (SELECT `bucket_date` FROM `weekly_rates` WHERE "+cmd+")",
		"DELETE FROM `blended_monthly_rates` WHERE `bucket_date` IN (SELECT `bucket_date` FROM `monthly_rates` WHERE "+cmd+")",
	)
}
