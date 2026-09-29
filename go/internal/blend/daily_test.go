package blend

// Ports spec/blended_rate_spec.rb.

import (
	"context"
	"database/sql"
	"slices"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

var ctx = context.Background()

func exec(t *testing.T, q db.Querier, query string, args ...any) {
	t.Helper()
	if _, err := q.ExecContext(ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

func queryInt(t *testing.T, q db.Querier, query string, args ...any) int {
	t.Helper()
	var n int
	if err := q.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func queryFloat(t *testing.T, q db.Querier, query string, args ...any) float64 {
	t.Helper()
	var f float64
	if err := q.QueryRowContext(ctx, query, args...).Scan(&f); err != nil {
		t.Fatalf("%s %v: %v", query, args, err)
	}
	return f
}

// queryDate returns a date (or "" for NULL) as stored text.
func queryDate(t *testing.T, q db.Querier, query string, args ...any) string {
	t.Helper()
	var d db.NullDate
	if err := q.QueryRowContext(ctx, query, args...).Scan(&d); err != nil {
		t.Fatal(err)
	}
	if !d.Valid {
		return ""
	}
	return db.FormatDate(d.Time)
}

func day(d time.Time) string { return db.FormatDate(d) }

func today() time.Time { return fixtures.Today() }

func rebuildDaily(t *testing.T, conn *sql.DB) {
	t.Helper()
	if err := RebuildDaily(ctx, conn, today()); err != nil {
		t.Fatal(err)
	}
}

func dailyReady(t *testing.T, conn *sql.DB) bool {
	t.Helper()
	ok, err := DailyReady(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func blendableBounds(t *testing.T, conn *sql.DB) (string, string) {
	t.Helper()
	scope, err := blendable(ctx, conn, rates.Daily)
	if err != nil {
		t.Fatal(err)
	}
	return queryDate(t, conn, scope.Columns("min(date)").SQL()), queryDate(t, conn, scope.Columns("max(date)").SQL())
}

// withChunkHook swaps the wrapChunk seam for the test.
func withChunkHook(t *testing.T, hook func(run func() error) error) {
	original := wrapChunk
	wrapChunk = hook
	t.Cleanup(func() { wrapChunk = original })
}

func insertRates(t *testing.T, q db.Querier, rows ...[]any) {
	t.Helper()
	for _, r := range rows {
		exec(t, q, "INSERT INTO rates (provider, date, base, quote, mid) VALUES (?, ?, ?, ?, ?)", r...)
	}
}

func TestRebuildMaterializesPivotBlend(t *testing.T) {
	conn := fixtures.New(t)
	rebuildDaily(t, conn)
	if n := queryInt(t, conn, "SELECT count(*) FROM blended_rates"); n == 0 {
		t.Fatal("empty")
	}

	date := fixtures.LatestDate()
	window, err := rates.Select(ctx, conn, "SELECT date, base, quote, provider, rate FROM rates WHERE date >= ? AND date <= ?",
		day(date.AddDate(0, 0, -rates.LookbackDays)), day(date))
	if err != nil {
		t.Fatal(err)
	}
	contributors := rates.CarryForward(window, date, rates.LookbackDays)
	var oracle float64
	for _, r := range currency.AnchorPegs(Blend(contributors, "USD", today()), "USD") {
		if r.Quote == "GBP" {
			oracle = r.Rate
		}
	}
	stored := queryFloat(t, conn, "SELECT rate FROM blended_rates WHERE quote = 'GBP' AND date = ?", day(date))
	if stored != oracle {
		t.Errorf("stored %v, oracle %v", stored, oracle)
	}
}

func TestRebuildStoresSparsely(t *testing.T) {
	conn := fixtures.New(t)
	d1, d2 := fixtures.BusinessDay(30), fixtures.BusinessDay(20)
	// The fake provider carries its own EUR to USD bridge so the pivot rebase can use its rows.
	insertRates(t, conn,
		[]any{"T1", day(d1), "EUR", "MXN", 20.0}, []any{"T1", day(d1), "EUR", "USD", 1.2},
		[]any{"T1", day(d2), "EUR", "MXN", 21.0}, []any{"T1", day(d2), "EUR", "USD", 1.2})
	rebuildDaily(t, conn)

	rows, err := conn.QueryContext(ctx, "SELECT date FROM blended_rates WHERE quote = 'MXN' ORDER BY date")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var d db.NullDate
		if err := rows.Scan(&d); err != nil {
			t.Fatal(err)
		}
		got = append(got, day(d.Time))
	}
	if want := []string{day(d1), day(d2)}; !slices.Equal(got, want) {
		t.Errorf("MXN dates = %v, want %v", got, want)
	}
}

func TestRebuildKeepsReadyThroughout(t *testing.T) {
	conn := fixtures.New(t)
	rebuildDaily(t, conn)
	if !dailyReady(t, conn) {
		t.Fatal("not ready after rebuild")
	}

	var observed []bool
	withChunkHook(t, func(run func() error) error {
		observed = append(observed, dailyReady(t, conn))
		err := run()
		observed = append(observed, dailyReady(t, conn))
		return err
	})
	rebuildDaily(t, conn)
	if len(observed) == 0 || slices.Contains(observed, false) {
		t.Errorf("ready states = %v", observed)
	}
}

func TestRebuildPrunesStaleLeadingRowsUpfront(t *testing.T) {
	conn := fixtures.New(t)
	rebuildDaily(t, conn)
	first, _ := blendableBounds(t, conn)
	firstDate, _ := db.ParseDate(first)
	staleEarly := day(firstDate.AddDate(0, 0, -10))
	exec(t, conn, "INSERT INTO blended_rates (date, quote, rate) VALUES (?, 'EUR', 1.0)", staleEarly)

	var initial *bool
	withChunkHook(t, func(run func() error) error {
		if initial == nil {
			ready := dailyReady(t, conn)
			initial = &ready
		}
		return run()
	})
	rebuildDaily(t, conn)
	if initial == nil || !*initial {
		t.Error("not ready when the first chunk started")
	}
	if !dailyReady(t, conn) {
		t.Error("not ready after rebuild")
	}
	if n := queryInt(t, conn, "SELECT count(*) FROM blended_rates WHERE date = ?", staleEarly); n != 0 {
		t.Errorf("stale rows = %d", n)
	}
}

func TestRebuildPrunesExactBoundaries(t *testing.T) {
	conn := fixtures.New(t)
	rebuildDaily(t, conn)
	first, last := blendableBounds(t, conn)
	firstDate, _ := db.ParseDate(first)
	lastDate, _ := db.ParseDate(last)
	prev, next := day(firstDate.AddDate(0, 0, -1)), day(lastDate.AddDate(0, 0, 1))
	exec(t, conn, "INSERT INTO blended_rates (date, quote, rate) VALUES (?, 'EUR', 1.0), (?, 'EUR', 1.0)", prev, next)
	rebuildDaily(t, conn)

	for d, want := range map[string]bool{prev: false, next: false, first: true, last: true} {
		n := queryInt(t, conn, "SELECT count(*) FROM blended_rates WHERE date = ?", d)
		if (n > 0) != want {
			t.Errorf("%s: %d rows", d, n)
		}
	}
}

func TestRebuildPrunesContractedBoundaries(t *testing.T) {
	conn := fixtures.New(t)
	rebuildDaily(t, conn)
	oldFirst, oldLast := blendableBounds(t, conn)
	exec(t, conn, "DELETE FROM rates WHERE date IN (?, ?)", oldFirst, oldLast)
	newFirst, newLast := blendableBounds(t, conn)
	rebuildDaily(t, conn)

	if n := queryInt(t, conn, "SELECT count(*) FROM blended_rates WHERE date IN (?, ?)", oldFirst, oldLast); n != 0 {
		t.Errorf("old boundary rows = %d", n)
	}
	if got := queryDate(t, conn, "SELECT min(date) FROM blended_rates"); got != newFirst {
		t.Errorf("min = %s, want %s", got, newFirst)
	}
	if got := queryDate(t, conn, "SELECT max(date) FROM blended_rates"); got != newLast {
		t.Errorf("max = %s, want %s", got, newLast)
	}
	if !dailyReady(t, conn) {
		t.Error("not ready")
	}
}

func TestRebuildSingleDateRange(t *testing.T) {
	conn := fixtures.New(t)
	single := day(fixtures.LatestDate())
	exec(t, conn, "DELETE FROM rates WHERE date != ?", single)
	rebuildDaily(t, conn)

	if n := queryInt(t, conn, "SELECT count(*) FROM blended_rates"); n == 0 {
		t.Error("empty")
	}
	if lo, hi := queryDate(t, conn, "SELECT min(date) FROM blended_rates"), queryDate(t, conn,
		"SELECT max(date) FROM blended_rates"); lo != single || hi != single {
		t.Errorf("range %s..%s", lo, hi)
	}
	if !dailyReady(t, conn) {
		t.Error("not ready")
	}
}

func TestRebuildClearsWithoutBlendableRates(t *testing.T) {
	conn := fixtures.New(t)
	rebuildDaily(t, conn)
	exec(t, conn, "DELETE FROM rates")
	rebuildDaily(t, conn)
	if n := queryInt(t, conn, "SELECT count(*) FROM blended_rates"); n != 0 {
		t.Errorf("rows = %d", n)
	}
	if dailyReady(t, conn) {
		t.Error("ready")
	}
}

func TestDailyReadyAfterPartialRefreshAndRebuild(t *testing.T) {
	conn := fixtures.New(t)
	if dailyReady(t, conn) {
		t.Error("ready while empty")
	}
	latest := fixtures.LatestDate()
	if err := RefreshDaily(ctx, conn, latest, latest, today()); err != nil {
		t.Fatal(err)
	}
	if n := queryInt(t, conn, "SELECT count(*) FROM blended_rates"); n == 0 {
		t.Error("partial refresh wrote nothing")
	}
	if dailyReady(t, conn) {
		t.Error("ready after a partial refresh")
	}
	rebuildDaily(t, conn)
	if !dailyReady(t, conn) {
		t.Error("not ready after rebuild")
	}
}

func TestRefreshRecomputesInsideWindowOnly(t *testing.T) {
	conn := fixtures.New(t)
	rebuildDaily(t, conn)
	date := fixtures.LatestDate()
	outside := day(fixtures.BusinessDay(30))
	eur := "SELECT rate FROM blended_rates WHERE quote = 'EUR' AND date = ?"
	beforeTarget, beforeOutside := queryFloat(t, conn, eur, day(date)), queryFloat(t, conn, eur, outside)

	// A late arrival shifts the contributor set for EUR at this anchor, close enough to the consensus that the outlier
	// filter keeps it.
	insertRates(t, conn, []any{"T1", day(date), "EUR", "USD", 1.10})
	if err := RefreshDaily(ctx, conn, date, date.AddDate(0, 0, rates.LookbackDays), today()); err != nil {
		t.Fatal(err)
	}
	if got := queryFloat(t, conn, eur, day(date)); got == beforeTarget {
		t.Error("target unchanged")
	}
	if got := queryFloat(t, conn, eur, outside); got != beforeOutside {
		t.Errorf("outside changed: %v -> %v", beforeOutside, got)
	}
}

func TestRefreshDropsAnchorsWithoutData(t *testing.T) {
	conn := fixtures.New(t)
	rebuildDaily(t, conn)
	date := fixtures.LatestDate()
	exec(t, conn, "DELETE FROM rates WHERE date = ?", day(date))
	if err := RefreshDaily(ctx, conn, date, date.AddDate(0, 0, rates.LookbackDays), today()); err != nil {
		t.Fatal(err)
	}
	if n := queryInt(t, conn, "SELECT count(*) FROM blended_rates WHERE date = ?", day(date)); n != 0 {
		t.Errorf("rows = %d", n)
	}
}

func TestChunksFollowRubyMonthShift(t *testing.T) {
	d := func(y int, m time.Month, dd int) time.Time { return time.Date(y, m, dd, 0, 0, 0, 0, time.UTC) }
	got := chunks(d(2024, 1, 31), d(2024, 12, 31))
	want := []span{
		{d(2024, 1, 31), d(2024, 4, 29)}, // Jan 31 >> 3 is Apr 30
		{d(2024, 4, 30), d(2024, 7, 29)},
		{d(2024, 7, 30), d(2024, 10, 29)},
		{d(2024, 10, 30), d(2024, 12, 31)},
	}
	if !slices.EqualFunc(got, want, func(a, b span) bool { return a.start.Equal(b.start) && a.end.Equal(b.end) }) {
		t.Errorf("chunks = %v", got)
	}
}
