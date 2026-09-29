package rates_test

import (
	"database/sql"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

func exec(t *testing.T, conn db.Querier, query string, args ...any) {
	t.Helper()
	if _, err := conn.ExecContext(ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

// window is Rate.where(date: (date - 14)..date).naked.all.
func window(t *testing.T, conn *sql.DB, date time.Time) []rates.Row {
	t.Helper()
	rows, err := rates.Select(ctx, conn, "SELECT date, base, quote, provider, rate FROM rates WHERE date BETWEEN ? AND ?",
		db.FormatDate(date.AddDate(0, 0, -14)), db.FormatDate(date))
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func dates(rows []rates.Row) []time.Time {
	var out []time.Time
	for _, r := range rows {
		if !slices.ContainsFunc(out, r.Date.Equal) {
			out = append(out, r.Date)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

func providers(rows []rates.Row) []string {
	var out []string
	for _, r := range rows {
		if !slices.Contains(out, r.Provider) {
			out = append(out, r.Provider)
		}
	}
	sort.Strings(out)
	return out
}

func TestCarryForwardReturnsLatestOnDate(t *testing.T) {
	conn := fixtures.New(t)
	date := fixtures.LatestDate()
	got := dates(rates.CarryForward(window(t, conn, date), date, rates.LookbackDays))
	if !got[len(got)-1].Equal(date) {
		t.Errorf("max date = %v, want %v", got[len(got)-1], date)
	}
}

func TestCarryForwardSnapsToNearestPriorDate(t *testing.T) {
	conn := fixtures.New(t)
	sunday := fixtures.RecentSunday()
	friday := fixtures.PrecedingFriday(sunday)
	rows, err := rates.Select(ctx, conn,
		"SELECT date, base, quote, provider, rate FROM rates WHERE provider = 'ECB' AND date BETWEEN ? AND ?",
		db.FormatDate(sunday.AddDate(0, 0, -14)), db.FormatDate(sunday))
	if err != nil {
		t.Fatal(err)
	}
	got := dates(rates.CarryForward(rows, sunday, rates.LookbackDays))
	if len(got) != 1 || !got[0].Equal(friday) {
		t.Errorf("dates = %v, want [%v]", got, friday)
	}
}

func TestCarryForwardIncludesEachProvider(t *testing.T) {
	conn := fixtures.New(t)
	date := fixtures.LatestDate()
	got := providers(rates.CarryForward(window(t, conn, date), date, rates.LookbackDays))
	if !slices.Contains(got, "ECB") || !slices.Contains(got, "BOC") {
		t.Errorf("providers = %v", got)
	}
}

func TestCarryForwardExcludesStaleProviders(t *testing.T) {
	conn := fixtures.New(t)
	date := fixtures.LatestDate()
	exec(t, conn, "INSERT INTO rates (date, base, quote, mid, provider) VALUES (?, 'EUR', 'XTS', 1.08, 'STALE')",
		db.FormatDate(date.AddDate(0, 0, -20)))
	got := providers(rates.CarryForward(window(t, conn, date), date, rates.LookbackDays))
	if !slices.Contains(got, "ECB") || slices.Contains(got, "STALE") {
		t.Errorf("providers = %v", got)
	}
}

func TestCarryForwardIncludesProvidersWithinLookback(t *testing.T) {
	conn := fixtures.New(t)
	date := fixtures.LatestDate()
	exec(t, conn, "INSERT INTO rates (date, base, quote, mid, provider) VALUES (?, 'USD', 'XTS', 0.92, 'FRED')",
		db.FormatDate(date.AddDate(0, 0, -10)))
	got := providers(rates.CarryForward(window(t, conn, date), date, rates.LookbackDays))
	if !slices.Contains(got, "ECB") || !slices.Contains(got, "FRED") {
		t.Errorf("providers = %v", got)
	}
}

func TestCarryForwardIncludesDifferentDatesWithinProvider(t *testing.T) {
	conn := fixtures.New(t)
	date := fixtures.LatestDate()
	exec(t, conn, "INSERT INTO rates (date, base, quote, mid, provider) VALUES (?, 'XTS', 'PLN', 0.05, 'ECB')",
		db.FormatDate(date.AddDate(0, 0, -3)))
	var found bool
	for _, r := range rates.CarryForward(window(t, conn, date), date, rates.LookbackDays) {
		found = found || (r.Provider == "ECB" && r.Quote == "PLN")
	}
	if !found {
		t.Error("no ECB PLN row")
	}
}

func TestCarryForwardBeforeDataset(t *testing.T) {
	conn := fixtures.New(t)
	date := time.Date(1901, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := rates.CarryForward(window(t, conn, date), date, rates.LookbackDays); len(got) != 0 {
		t.Errorf("got %d rows", len(got))
	}
}

func TestCarryForwardClientAheadOfServer(t *testing.T) {
	conn := fixtures.New(t)
	today := fixtures.Today()
	future := today.AddDate(0, 0, 1)
	ahead := rates.CarryForward(window(t, conn, future), future, rates.LookbackDays)
	if len(ahead) == 0 {
		t.Fatal("no rows")
	}
	current := rates.CarryForward(window(t, conn, today), today, rates.LookbackDays)
	if !slices.EqualFunc(dates(ahead), dates(current), time.Time.Equal) {
		t.Errorf("dates %v, want %v", dates(ahead), dates(current))
	}
}

func TestEachSnapshotMatchesCarryForward(t *testing.T) {
	conn := fixtures.New(t)
	date := fixtures.LatestDate()
	rows, err := rates.Select(ctx, conn, "SELECT date, base, quote, provider, rate FROM rates WHERE date >= ?",
		db.FormatDate(date.AddDate(0, 0, -40)))
	if err != nil {
		t.Fatal(err)
	}
	var anchors []time.Time
	for d := date.AddDate(0, 0, -20); !d.After(date.AddDate(0, 0, 2)); d = d.AddDate(0, 0, 1) {
		anchors = append(anchors, d)
	}
	rates.EachSnapshot(rows, anchors, rates.LookbackDays, func(d time.Time, got []rates.Row) {
		want := rates.CarryForward(rows, d, rates.LookbackDays)
		key := func(r rates.Row) string { return r.Provider + r.Base + r.Quote + db.FormatDate(r.Date) }
		g, w := make([]string, len(got)), make([]string, len(want))
		for i := range got {
			g[i] = key(got[i])
		}
		for i := range want {
			w[i] = key(want[i])
		}
		sort.Strings(g)
		sort.Strings(w)
		if !slices.Equal(g, w) {
			t.Errorf("%v: snapshot differs from CarryForward", d)
		}
	})
}

// betweenDates is Rate.between(start..end).map(:date).
func betweenDates(t *testing.T, conn *sql.DB, start, end time.Time) []time.Time {
	t.Helper()
	q := rates.Daily.Dataset().Between(start, end, fixtures.Today())
	return scanDates(t, conn, q.Columns("date").SQL())
}

func scanDates(t *testing.T, conn *sql.DB, query string) []time.Time {
	t.Helper()
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var d db.NullDate
		if err := rows.Scan(&d); err != nil {
			t.Fatal(err)
		}
		out = append(out, d.Time)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBetweenReturnsRatesBetweenDates(t *testing.T) {
	conn := fixtures.New(t)
	start, end := fixtures.LatestDate().AddDate(0, 0, -30), fixtures.LatestDate()
	got := betweenDates(t, conn, start, end)
	if got[0].After(start) || !got[len(got)-1].Equal(end) {
		t.Errorf("first %v last %v", got[0], got[len(got)-1])
	}
}

func TestBetweenStartsOnPrecedingBusinessDay(t *testing.T) {
	conn := fixtures.New(t)
	sunday := fixtures.RecentSunday()
	friday := fixtures.PrecedingFriday(sunday)
	if got := betweenDates(t, conn, sunday, sunday.AddDate(0, 0, 1)); !slices.ContainsFunc(got, friday.Equal) {
		t.Errorf("dates %v lack %v", got, friday)
	}
}

func TestBetweenBeforeDataset(t *testing.T) {
	conn := fixtures.New(t)
	start := time.Date(1901, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := betweenDates(t, conn, start, start.AddDate(0, 0, 30)); len(got) != 0 {
		t.Errorf("got %d rows", len(got))
	}
}

func TestBetweenAllowsStartBeforeDataset(t *testing.T) {
	conn := fixtures.New(t)
	if got := betweenDates(t, conn, time.Date(1901, 1, 1, 0, 0, 0, 0, time.UTC), fixtures.LatestDate()); len(got) == 0 {
		t.Error("no rows")
	}
}

func TestBetweenFutureStart(t *testing.T) {
	conn := fixtures.New(t)
	start := fixtures.Today().AddDate(0, 0, 1)
	if got := betweenDates(t, conn, start, start.AddDate(0, 0, 1)); len(got) != 0 {
		t.Errorf("got %d rows", len(got))
	}
}

// only is Rate.where(date: (date - 14)..date).only(currencies...), as base, quote and provider.
func only(t *testing.T, conn *sql.DB, currencies ...string) [][3]string {
	t.Helper()
	date := fixtures.LatestDate()
	q := rates.Daily.Dataset().
		Filter("date BETWEEN " + db.LitDate(date.AddDate(0, 0, -14)) + " AND " + db.LitDate(date)).
		Only(currencies...)
	rows, err := conn.QueryContext(ctx, "SELECT base, quote, provider FROM ("+q.SQL()+")")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [][3]string
	for rows.Next() {
		var r [3]string
		if err := rows.Scan(&r[0], &r[1], &r[2]); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func onlyProviders(rows [][3]string) []string {
	var out []string
	for _, r := range rows {
		if !slices.Contains(out, r[2]) {
			out = append(out, r[2])
		}
	}
	return out
}

func TestOnlyReturnsPairsInvolvingCurrencies(t *testing.T) {
	conn := fixtures.New(t)
	rows := only(t, conn, "CAD", "USD")
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	for _, r := range rows {
		if !slices.Contains([]string{"CAD", "USD"}, r[0]) && !slices.Contains([]string{"CAD", "USD"}, r[1]) {
			t.Errorf("row %v", r)
		}
	}
}

func TestOnlyIncludesCurrencyAsBase(t *testing.T) {
	if got := onlyProviders(only(t, fixtures.New(t), "USD", "EUR")); !slices.Contains(got, "BOC") {
		t.Errorf("providers = %v", got)
	}
}

func TestOnlyIncludesProvidersSpanningBothSides(t *testing.T) {
	if got := onlyProviders(only(t, fixtures.New(t), "JPY", "EUR")); !slices.Contains(got, "BOJ") {
		t.Errorf("providers = %v", got)
	}
}

func TestOnlyNoMatches(t *testing.T) {
	conn := fixtures.New(t)
	var n int
	if err := conn.QueryRow("SELECT count(*) FROM (" + rates.Daily.Dataset().Only("FOO").SQL() + ")").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("got %d rows", n)
	}
}

func downsampleDates(t *testing.T, conn *sql.DB, p rates.Precision) []time.Time {
	latest := fixtures.LatestDate()
	q := rates.Daily.Dataset().Between(latest.AddDate(0, 0, -366), latest, fixtures.Today())
	return scanDates(t, conn, "SELECT date FROM ("+q.Downsample(p)+")")
}

func distinct(ds []time.Time) int {
	seen := map[time.Time]bool{}
	for _, d := range ds {
		seen[d] = true
	}
	return len(seen)
}

func TestDownsampleGroupsByWeek(t *testing.T) {
	if n := distinct(downsampleDates(t, fixtures.New(t), rates.Week)); n > 55 || n == 0 {
		t.Errorf("%d weeks", n)
	}
}

func TestDownsampleGroupsByMonth(t *testing.T) {
	if n := distinct(downsampleDates(t, fixtures.New(t), rates.Month)); n > 14 || n == 0 {
		t.Errorf("%d months", n)
	}
}

func TestDownsampleSortsByDate(t *testing.T) {
	got := downsampleDates(t, fixtures.New(t), rates.Week)
	if !sort.SliceIsSorted(got, func(i, j int) bool { return got[i].Before(got[j]) }) {
		t.Error("not sorted")
	}
}
