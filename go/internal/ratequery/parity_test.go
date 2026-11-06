package ratequery

import (
	"database/sql"
	"slices"
	"strings"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/blend"
	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// spec/blend_parity_spec.rb and spec/blend_parity_carveouts_spec.rb.

func rebuildAll(t *testing.T, conn *sql.DB) {
	t.Helper()
	rebuildDaily(t, conn)
	for _, m := range blend.Rollups {
		rebuildRollup(t, conn, m)
	}
}

func runParity(t *testing.T, conn *sql.DB, samples int, seed uint64) ParityReport {
	t.Helper()
	report, err := Parity(ctx, conn, samples, seed, today())
	if err != nil {
		t.Fatal(err)
	}
	return report
}

// Merge-blocking parity gate for the materialized blend (#570): both paths
// serve identical responses across generated shapes, with exactly two declared
// behavior changes, each asserted rather than ignored.
func TestParityServesIdenticalResponsesFromTables(t *testing.T) {
	conn := fixtures.New(t)
	rebuildAll(t, conn)
	report := runParity(t, conn, 30, 20260723)
	t.Log(report)
	if len(report.Failures) != 0 || !report.Passed() {
		t.Fatalf("report:\n%s", report)
	}
	for _, group := range []string{"week", "month"} {
		c := report.GroupedCoverage[group]
		if c.Materialized == 0 || c.Fallback != 0 {
			t.Errorf("%s coverage = %+v", group, *c)
		}
	}
	if report.Shapes <= 30 {
		t.Fatalf("%d shapes", report.Shapes)
	}
}

func TestGroupedParityRejectsChangedStoredValues(t *testing.T) {
	conn := fixtures.New(t)
	rebuildAll(t, conn)
	for _, m := range blend.Rollups {
		exec(t, conn, "UPDATE "+m.Table+" SET rate = 99.0 WHERE quote = 'GBP'")
	}
	report := runParity(t, conn, 0, 42)
	var groups []string
	for _, f := range report.Failures {
		groups = append(groups, f.Shape["group"])
	}
	if !slices.Contains(groups, "week") || !slices.Contains(groups, "month") {
		t.Fatalf("failing groups = %v", groups)
	}
	if report.SnapbackRows != 0 {
		t.Fatalf("snap-back rows = %d", report.SnapbackRows)
	}
}

func TestParityRejectsLiveVersusLiveWhenGroupedTablesEmpty(t *testing.T) {
	conn := fixtures.New(t)
	rebuildAll(t, conn)
	for _, m := range blend.Rollups {
		exec(t, conn, "DELETE FROM "+m.Table)
	}
	report := runParity(t, conn, 0, 42)
	if report.Passed() || len(report.Failures) != 0 {
		t.Fatalf("report:\n%s", report)
	}
	for _, group := range []string{"week", "month"} {
		if c := report.GroupedCoverage[group]; c.Materialized != 0 || c.Fallback == 0 {
			t.Errorf("%s coverage = %+v", group, *c)
		}
	}
	if len(report.Incomplete) == 0 || !strings.Contains(report.String(), "INCOMPLETE") {
		t.Fatalf("report:\n%s", report)
	}
}

func TestParityDoesNotCountEmptyResultsAsMaterialized(t *testing.T) {
	conn := fixtures.New(t)
	rebuildAll(t, conn)
	for _, m := range blend.Rollups {
		exec(t, conn, "DELETE FROM "+m.Source.Name)
		exec(t, conn, "DELETE FROM "+m.Table)
	}
	report := runParity(t, conn, 0, 42)
	if report.Passed() || len(report.Failures) != 0 {
		t.Fatalf("report:\n%s", report)
	}
	for _, group := range []string{"week", "month"} {
		if c := report.GroupedCoverage[group]; c.Materialized != 0 || c.Fallback != 0 || c.Empty == 0 {
			t.Errorf("%s coverage = %+v", group, *c)
		}
	}
}

func TestParityReportsPartialCoverageApartFromMismatches(t *testing.T) {
	conn := fixtures.New(t)
	rebuildAll(t, conn)
	for _, m := range blend.Rollups {
		exec(t, conn, "DELETE FROM "+m.Table+" WHERE bucket_date = (SELECT max(bucket_date) FROM "+m.Table+")")
	}
	report := runParity(t, conn, 0, 42)
	if report.Passed() || len(report.Failures) != 0 {
		t.Fatalf("report:\n%s", report)
	}
	for _, group := range []string{"week", "month"} {
		if c := report.GroupedCoverage[group]; c.Materialized == 0 || c.Fallback == 0 {
			t.Errorf("%s coverage = %+v", group, *c)
		}
	}
	if len(report.Incomplete) == 0 {
		t.Fatal("nothing incomplete")
	}
}

func TestParityReportsEmptyUSDBlendsAsUnverifiedFallback(t *testing.T) {
	conn := fixtures.New(t)
	rebuildAll(t, conn)
	for _, m := range blend.Rollups {
		var bucket string
		if err := conn.QueryRowContext(ctx, "SELECT max(bucket_date) FROM "+m.Source.Name).Scan(&bucket); err != nil {
			t.Fatal(err)
		}
		exec(t, conn, "DELETE FROM "+m.Source.Name+" WHERE bucket_date = ?", bucket)
		exec(t, conn, "INSERT INTO "+m.Source.Name+" (bucket_date, provider, base, quote, rate) VALUES (?, 'ECB', 'EUR', 'JPY', 150.0)", bucket)
		if _, err := m.Refresh(ctx, conn, []string{bucket}, today()); err != nil {
			t.Fatal(err)
		}
	}
	report := runParity(t, conn, 0, 42)
	if report.Passed() || len(report.Failures) != 0 || len(report.Incomplete) == 0 {
		t.Fatalf("report:\n%s", report)
	}
	if !strings.Contains(report.String(), "legitimately empty USD blends") {
		t.Fatalf("report:\n%s", report)
	}
}

// Carve-outs.

func liveQuery(t *testing.T, conn *sql.DB, kv ...string) []Record {
	t.Helper()
	q := newQuery(t, conn, kv...)
	q.ForceLive = true
	return all(t, q)
}

// agingScenario: T2's MXN observation ages out of the carry-forward lookback
// between T1's observation date and the range start, so the live snap-back at
// the range start drops it while the table keeps the value blended at T1's
// date.
func agingScenario(t *testing.T) (conn *sql.DB, observed, rangeStart string) {
	conn = fixtures.New(t)
	obs := fixtures.BusinessDay(40)
	stale := obs.AddDate(0, 0, -10)
	insert(t, conn, "T1", obs, "EUR", "MXN", 20.0)
	insert(t, conn, "T1", obs, "EUR", "USD", 1.2)
	insert(t, conn, "T2", stale, "EUR", "MXN", 40.0)
	insert(t, conn, "T2", stale, "EUR", "USD", 1.2)
	rebuildDaily(t, conn)
	return conn, d(obs), d(obs.AddDate(0, 0, 12))
}

// The explain machinery needs exercising: fixture shapes are usually identical,
// so without an engineered divergence a broken verifier would only surface
// against a production copy.
func TestParityVerifiesEngineeredDivergencesAndRejectsTampering(t *testing.T) {
	conn, observed, rangeStart := agingScenario(t)
	start, _ := ParseDate(rangeStart)
	shape := Shape{"from": rangeStart, "to": d(start.AddDate(0, 0, 2)), "quotes": "MXN"}

	harness := func() *parity {
		p := &parity{conn: conn, today: today(), slots: DefaultSlots}
		p.q, p.frameCache = conn, map[string]map[[2]string]Number{}
		return p
	}
	h := harness()
	table, err := h.records(ctx, shape, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	live, err := h.records(ctx, shape, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if same, _ := sameJSON(table, live); same {
		t.Fatal("table and live agree; the scenario engineered nothing")
	}
	verified, reason, err := h.explainDivergence(ctx, shape, table, live)
	if err != nil || reason != "" || verified == 0 {
		t.Fatalf("verified %d, reason %q, err %v", verified, reason, err)
	}

	exec(t, conn, "UPDATE blended_rates SET rate = 999.0 WHERE quote = 'MXN' AND date = ?", observed)
	tampered := harness()
	tamperedTable, err := tampered.records(ctx, shape, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, reason, err := tampered.explainDivergence(ctx, shape, tamperedTable, live); err != nil || reason == "" {
		t.Fatalf("tampered table accepted (err %v)", err)
	}
}

// Carve-out 1: a snap-back row serves the canonical anchor-date value, asserted
// in the pivot frame.
func TestSnapBackRowsServeCanonicalAnchorDateValue(t *testing.T) {
	conn, observed, rangeStart := agingScenario(t)
	start, _ := ParseDate(rangeStart)
	shape := []string{"from", rangeStart, "to", d(start.AddDate(0, 0, 2)), "quotes", "MXN", "base", "USD"}
	tableRow := find(query(t, conn, shape...), "MXN")
	liveRow := find(liveQuery(t, conn, shape...), "MXN")
	canonical := find(liveQuery(t, conn, "from", observed, "to", observed, "quotes", "MXN", "base", "USD"), "MXN")
	if tableRow.Date != observed {
		t.Fatalf("table row dated %s", tableRow.Date)
	}
	// By the range start T2 has aged out of the lookback, so the live snap-back
	// drops it.
	if liveRow.Rate == canonical.Rate || tableRow.Rate != canonical.Rate {
		t.Fatalf("table %v, live %v, canonical %v", tableRow.Rate, liveRow.Rate, canonical.Rate)
	}
}

// Carve-out 1, existence variant: an observation the consensus filter masked at
// its own anchor has no canonical value. Live can surface it once the masking
// cohort ages out of the lookback; the table never serves it.
func TestOmitsRowsConsensusMaskedAtTheirOwnAnchor(t *testing.T) {
	conn := fixtures.New(t)
	d0 := fixtures.BusinessDay(40)
	d1 := d0.AddDate(0, 0, 9)
	for _, c := range []struct {
		key  string
		rate float64
	}{{"C1", 19.0}, {"C2", 19.1}, {"C3", 18.9}, {"C4", 19.05}} {
		insert(t, conn, c.key, d0, "EUR", "ZAR", c.rate)
		insert(t, conn, c.key, d0, "EUR", "USD", 1.08)
	}
	insert(t, conn, "X", d1, "EUR", "ZAR", 99.0)
	insert(t, conn, "X", d1, "EUR", "USD", 1.08)
	rebuildDaily(t, conn)

	shape := []string{"from", d(d0.AddDate(0, 0, -2)), "to", d(d1.AddDate(0, 0, 14)), "quotes", "ZAR", "base", "USD"}
	zarDates := func(records []Record) []string {
		var out []string
		for _, r := range records {
			if r.Quote == "ZAR" {
				out = append(out, r.Date)
			}
		}
		return out
	}
	live, table := zarDates(liveQuery(t, conn, shape...)), zarDates(query(t, conn, shape...))
	if !slices.Contains(live, d(d1)) || !slices.Contains(table, d(d0)) || slices.Contains(table, d(d1)) {
		t.Fatalf("live %v, table %v", live, table)
	}
	own := zarDates(liveQuery(t, conn, "from", d(d1), "to", d(d1), "quotes", "ZAR", "base", "USD"))
	if slices.Contains(own, d(d1)) {
		t.Fatalf("own anchor emits %v", own)
	}
}

// Carve-out 2: range batches always blend via the pivot. A batch whose rows all
// share the requested base used to blend directly in that base, where consensus
// and weighting see differently shaped numbers.
func TestBlendsRangeBatchesInPivotFrame(t *testing.T) {
	conn := fixtures.New(t)
	eraStart, eraEnd := fixtures.BusinessDay(100), fixtures.BusinessDay(80)
	exec(t, conn, "DELETE FROM rates WHERE date >= ? AND date <= ? AND provider != 'ECB'",
		d(eraStart.AddDate(0, 0, -rates.LookbackDays)), d(eraEnd))
	for _, date := range []string{d(eraStart), d(eraEnd)} {
		exec(t, conn, "INSERT INTO rates (provider, date, base, quote, mid) VALUES ('T3', ?, 'EUR', 'GBP', 0.95), ('T3', ?, 'EUR', 'USD', 1.30)",
			date, date)
	}
	rebuildDaily(t, conn)

	window, err := rates.Select(ctx, conn, "SELECT date, base, quote, provider, rate FROM rates WHERE date >= ? AND date <= ?",
		d(eraStart.AddDate(0, 0, -rates.LookbackDays)), d(eraStart))
	if err != nil {
		t.Fatal(err)
	}
	contributors := rates.CarryForward(window, eraStart, rates.LookbackDays)
	for _, r := range contributors {
		if r.Base != "EUR" {
			t.Fatalf("contributor based on %s", r.Base)
		}
	}
	fast := findBlended(currency.AnchorPegs(blend.Blend(contributors, "EUR", today()), "EUR"), "GBP")
	pivot := findBlended(derive(currency.AnchorPegs(blend.Blend(contributors, "USD", today()), "USD"), "EUR"), "GBP")
	// Precondition: the two frames genuinely disagree for this batch, beyond
	// rounding.
	if roundValue(pivot.Rate) == roundValue(fast.Rate) {
		t.Fatalf("frames agree: %v", pivot.Rate)
	}
	shape := []string{"from", d(eraStart), "to", d(eraStart), "quotes", "GBP"}
	for _, records := range [][]Record{query(t, conn, shape...), liveQuery(t, conn, shape...)} {
		if gbp := find(records, "GBP"); gbp.Rate.Value != roundValue(pivot.Rate) {
			t.Errorf("GBP = %v, pivot frame %v", gbp.Rate.Value, roundValue(pivot.Rate))
		}
	}
}

func findBlended(rows []currency.Blended, quote string) currency.Blended {
	for _, r := range rows {
		if r.Quote == quote {
			return r
		}
	}
	return currency.Blended{}
}
