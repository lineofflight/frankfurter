package blend

// From spec/blend_parity_carveouts_spec.rb, at the table level. The query-level
// comparisons (table path versus live path through RateQuery, BlendParity's
// explain_divergence, pivot-frame derive) need the API step's RateQuery.

import (
	"database/sql"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// liveBlend is the live pipeline at one anchor: the carry-forward snapshot of
// blendable rows, blended to the pivot and peg-anchored, by quote.
func liveBlend(t *testing.T, conn *sql.DB, anchor time.Time) map[string]currency.Blended {
	t.Helper()
	scope, err := blendable(ctx, conn, rates.Daily)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := rates.Select(ctx, conn, scope.Filter("date >= "+db.LitDate(anchor.AddDate(0, 0, -rates.LookbackDays))+
		" AND date <= "+db.LitDate(anchor)).Columns("date, base, quote, provider, rate").SQL())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]currency.Blended{}
	snapshot := rates.CarryForward(rows, anchor, rates.LookbackDays)
	for _, b := range currency.AnchorPegs(Blend(snapshot, Pivot, today()), Pivot) {
		out[b.Quote] = b
	}
	return out
}

// A stored row keeps the value blended at its own date, even after a
// contributor ages out of the lookback before a later range start.
func TestStoredRowsKeepCanonicalAnchorValue(t *testing.T) {
	conn := fixtures.New(t)
	observed := fixtures.BusinessDay(40)
	stale := observed.AddDate(0, 0, -10)
	rangeStart := observed.AddDate(0, 0, 12)
	// Each fake provider carries its own EUR to USD bridge so the pivot rebase
	// can use its rows.
	insertRates(t, conn,
		[]any{"T1", day(observed), "EUR", "MXN", 20.0}, []any{"T1", day(observed), "EUR", "USD", 1.2},
		[]any{"T2", day(stale), "EUR", "MXN", 40.0}, []any{"T2", day(stale), "EUR", "USD", 1.2})
	rebuildDaily(t, conn)

	stored := queryFloat(t, conn, "SELECT rate FROM blended_rates WHERE quote = 'MXN' AND date = ?", day(observed))
	canonical := liveBlend(t, conn, observed)["MXN"]
	aged := liveBlend(t, conn, rangeStart)["MXN"]
	if !canonical.Date.Equal(observed) || !aged.Date.Equal(observed) {
		t.Fatalf("dates %v, %v", canonical.Date, aged.Date)
	}
	// By the range start T2 has aged out, so the live snap-back drops it.
	if aged.Rate == canonical.Rate {
		t.Error("aged value equals canonical")
	}
	if stored != canonical.Rate {
		t.Errorf("stored %v, canonical %v", stored, canonical.Rate)
	}
}

// An observation the consensus filter masked at its own anchor has no canonical
// value, so the table never stores it, even though live snapshots surface it
// once the masking cohort ages out.
func TestStoredRowsOmitConsensusMaskedObservations(t *testing.T) {
	conn := fixtures.New(t)
	d0 := fixtures.BusinessDay(40)
	d1 := d0.AddDate(0, 0, 9)
	for _, c := range []struct {
		provider string
		rate     float64
	}{{"C1", 19.0}, {"C2", 19.1}, {"C3", 18.9}, {"C4", 19.05}} {
		insertRates(t, conn, []any{c.provider, day(d0), "EUR", "ZAR", c.rate},
			[]any{c.provider, day(d0), "EUR", "USD", 1.08})
	}
	insertRates(t, conn, []any{"X", day(d1), "EUR", "ZAR", 99.0}, []any{"X", day(d1), "EUR", "USD", 1.08})
	rebuildDaily(t, conn)

	has := func(d time.Time) bool {
		return queryInt(t, conn, "SELECT count(*) FROM blended_rates WHERE quote = 'ZAR' AND date = ?", day(d)) > 0
	}
	if !has(d0) || has(d1) {
		t.Errorf("stored d0 %v, d1 %v", has(d0), has(d1))
	}
	if b, ok := liveBlend(t, conn, d1)["ZAR"]; ok && b.Date.Equal(d1) {
		t.Error("ZAR unmasked at its own anchor")
	}
	if b := liveBlend(t, conn, d1.AddDate(0, 0, 14))["ZAR"]; !b.Date.Equal(d1) {
		t.Errorf("live snapshot after the cohort aged out: %+v", b)
	}
}
