package provider

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/blend"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// Materialized is the stored blend of internal/blend behind this package's Blend and internal/schedule's Blend. The
// Tx methods join the caller's transaction; the others run their own on DB.
type Materialized struct {
	DB *sql.DB

	// Today defaults to rates.Today.
	Today func() time.Time
}

func (m Materialized) today() time.Time {
	if m.Today != nil {
		return m.Today()
	}
	return rates.Today()
}

// RefreshTx implements Blend with BlendedRate.refresh.
func (m Materialized) RefreshTx(ctx context.Context, q db.Querier, from, to time.Time) error {
	return blend.RefreshDaily(ctx, q, from, to, m.today())
}

// RefreshRollupsTx implements Blend with BlendedWeeklyRate.refresh and BlendedMonthlyRate.refresh.
func (m Materialized) RefreshRollupsTx(ctx context.Context, q db.Querier, buckets map[rates.Precision][]string) error {
	for _, r := range blend.Rollups {
		if _, err := r.Refresh(ctx, q, buckets[r.Source.Precision], m.today()); err != nil {
			return err
		}
	}
	return nil
}

// Refresh recomputes the stored daily blends for [from, to] in their own transactions.
func (m Materialized) Refresh(ctx context.Context, from, to time.Time) error {
	return blend.RefreshDaily(ctx, m.DB, from, to, m.today())
}

// Ready is BlendedRate.ready?.
func (m Materialized) Ready(ctx context.Context) (bool, error) { return blend.DailyReady(ctx, m.DB) }

// Rebuild is BlendedRate.rebuild.
func (m Materialized) Rebuild(ctx context.Context) error {
	return blend.RebuildDaily(ctx, m.DB, m.today())
}

// Populate is BlendedWeeklyRate.populate (rates.Week) or BlendedMonthlyRate.populate (rates.Month).
func (m Materialized) Populate(ctx context.Context, p rates.Precision) (int, error) {
	for _, r := range blend.Rollups {
		if r.Source.Precision == p {
			return r.Populate(ctx, m.DB, m.today())
		}
	}
	return 0, fmt.Errorf("no blended rollup at precision %v", p)
}
