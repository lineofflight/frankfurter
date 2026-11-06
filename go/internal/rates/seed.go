package rates

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/seeds"
)

// SeedProviders is Provider.seed (the db:seed task): it replaces the providers
// table with db/seeds/providers, then restores coverage for any excluded code
// the Money gem has since learned to name. Provider metadata is config as data,
// so every boot reseeds.
func SeedProviders(ctx context.Context, conn *sql.DB) error {
	providers, err := seeds.Providers()
	if err != nil {
		return err
	}
	return db.Immediate(ctx, conn, func(q db.Querier) error {
		if _, err := q.ExecContext(ctx, "DELETE FROM providers"); err != nil {
			return err
		}
		for _, p := range providers {
			_, err := q.ExecContext(ctx, `INSERT INTO providers (key, name, data_url, terms_url, coverage_start,
				pivot_currency, rate_type, country_code, publish_schedule, publish_cadence, frequency)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				p.Key, p.Name, null(p.DataURL), null(p.TermsURL), null(p.CoverageStart), null(p.PivotCurrency),
				null(p.RateType), null(p.CountryCode), null(p.PublishSchedule), null(p.PublishCadence), p.Frequency)
			if err != nil {
				return fmt.Errorf("seed provider %s: %w", p.Key, err)
			}
		}
		return restoreRecognized(ctx, q)
	})
}

func restoreRecognized(ctx context.Context, q db.Querier) error {
	rows, err := q.QueryContext(ctx, "SELECT DISTINCT iso_code FROM currency_exclusions ORDER BY iso_code")
	if err != nil {
		return err
	}
	var recognized []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			rows.Close()
			return err
		}
		if currency.Named(code) {
			recognized = append(recognized, code)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(recognized) == 0 {
		return err
	}

	list := db.LitList(recognized)
	counterparts, err := pairCodes(ctx, q, "SELECT DISTINCT base, quote FROM rates WHERE (base IN "+list+
		") OR (quote IN "+list+")")
	if err != nil {
		return err
	}
	return RefreshSummaries(ctx, q, append(recognized, counterparts...), "")
}

func null(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Today is Ruby's Date.today: the local calendar date, as UTC midnight like
// every other date here.
func Today() time.Time {
	y, m, d := time.Now().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
