package blend

// Ports spec/rollup_spec.rb. Its RateQuery cases (boundary buckets, single-date
// grouped queries) are checked here at the table level through Rollup.Read,
// which is what a grouped RateQuery serves from once the tables are ready; the
// API step owns the query-level versions and the cache-key case.

import (
	"database/sql"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

type rollupRow struct {
	date                  string
	provider, base, quote string
	rate                  float64
}

// rollupRows runs a query yielding base, provider, quote, rate and date.
func rollupRows(t *testing.T, conn *sql.DB, query string) []rollupRow {
	t.Helper()
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []rollupRow
	for rows.Next() {
		var r rollupRow
		var d db.NullDate
		if err := rows.Scan(&r.base, &r.provider, &r.quote, &r.rate, &d); err != nil {
			t.Fatal(err)
		}
		r.date = day(d.Time)
		out = append(out, r)
	}
	return out
}

func downsampled(t *testing.T, conn *sql.DB, p rates.Precision, start, end time.Time) []rollupRow {
	return rollupRows(t, conn, rates.Daily.Dataset().Between(start, end, today()).Downsample(p))
}

func rollupTable(t *testing.T, conn *sql.DB, table rates.Table, start, end time.Time) []rollupRow {
	q := table.Dataset().Between(start, end, today()).Columns("base, provider, quote, rate, bucket_date")
	return rollupRows(t, conn, q.SQL())
}

func seriesKeys(rows []rollupRow) []string {
	var out []string
	for _, r := range rows {
		k := r.provider + " " + r.base + " " + r.quote
		if !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

func sortRows(rows []rollupRow) {
	slices.SortFunc(rows, func(a, b rollupRow) int {
		ka, kb := a.date+a.provider+a.base+a.quote, b.date+b.provider+b.base+b.quote
		switch {
		case ka < kb:
			return -1
		case ka > kb:
			return 1
		}
		return 0
	})
}

func TestRollupsCoverDownsampleSeries(t *testing.T) {
	conn := fixtures.New(t)
	end := fixtures.LatestDate()
	start := end.AddDate(0, 0, -366)
	for _, table := range rates.Rollups {
		want := seriesKeys(downsampled(t, conn, table.Precision, start, end))
		if got := seriesKeys(rollupTable(t, conn, table, start, end)); !slices.Equal(got, want) {
			t.Errorf("%s: series %v, want %v", table.Name, got, want)
		}
	}
}

func TestWeeklyRollupMatchesDownsampleForInnerBuckets(t *testing.T) {
	conn := fixtures.New(t)
	end := fixtures.LatestDate()
	start := end.AddDate(0, 0, -366)
	ds := downsampled(t, conn, rates.Week, start, end)
	sortRows(ds)
	var buckets []string
	for _, r := range ds {
		if !slices.Contains(buckets, r.date) {
			buckets = append(buckets, r.date)
		}
	}
	slices.Sort(buckets)
	// Boundary buckets are skipped: partial-bucket averages differ.
	inner := buckets[1 : len(buckets)-1]
	filter := func(rows []rollupRow) []rollupRow {
		var out []rollupRow
		for _, r := range rows {
			if slices.Contains(inner, r.date) {
				out = append(out, r)
			}
		}
		return out
	}
	want := filter(ds)
	got := filter(rollupTable(t, conn, rates.Weekly, start, end))
	sortRows(got)
	if len(got) != len(want) {
		t.Fatalf("%d rows, want %d", len(got), len(want))
	}
	for i := range got {
		if math.Abs(got[i].rate-want[i].rate) > 0.0001 {
			t.Errorf("%+v, want %+v", got[i], want[i])
		}
	}
}

func TestIncrementalRefreshRebuildsAffectedBuckets(t *testing.T) {
	for _, tc := range []struct {
		table string
		mid   float64
	}{{"weekly_rates", 42.0}, {"monthly_rates", 99.0}} {
		t.Run(tc.table, func(t *testing.T) {
			conn := fixtures.New(t)
			date := fixtures.LatestDate()
			insertRates(t, conn, []any{"ECB", day(date), "EUR", "XTS", tc.mid})
			refreshProvider(t, conn, "ECB", date)
			got := queryFloat(t, conn, "SELECT rate FROM "+tc.table+" WHERE provider = 'ECB' AND quote = 'XTS'")
			if math.Abs(got-tc.mid) > 0.01 {
				t.Errorf("rate = %v", got)
			}
			// Unrelated provider buckets untouched.
			if n := queryInt(t, conn, "SELECT count(*) FROM "+tc.table+" WHERE provider = 'BOC'"); n == 0 {
				t.Error("BOC rows gone")
			}
		})
	}
}

func TestRollupScopes(t *testing.T) {
	conn := fixtures.New(t)
	end := fixtures.LatestDate()
	for _, tc := range []struct {
		table    rates.Table
		lookback int
		only     []string
	}{{rates.Weekly, 60, []string{"USD", "GBP"}}, {rates.Monthly, 180, []string{"USD"}}} {
		t.Run(tc.table.Name, func(t *testing.T) {
			ecb := tc.table.Dataset().Filter("provider = 'ECB'")
			var providers []string
			for _, r := range rollupRows(t, conn, ecb.Columns("base, provider, quote, rate, bucket_date").SQL()) {
				if !slices.Contains(providers, r.provider) {
					providers = append(providers, r.provider)
				}
			}
			if !slices.Equal(providers, []string{"ECB"}) {
				t.Errorf("ecb providers = %v", providers)
			}

			start := end.AddDate(0, 0, -tc.lookback)
			between := rollupTable(t, conn, tc.table, start, end)
			if len(between) == 0 {
				t.Error("between is empty")
			}
			if tc.table == rates.Weekly {
				for _, r := range between {
					if r.date < day(start.AddDate(0, 0, -7)) {
						t.Errorf("bucket %s before %s", r.date, day(start.AddDate(0, 0, -7)))
					}
				}
			}

			only := ecb.Only(tc.only...).Columns("base, provider, quote, rate, bucket_date")
			for _, r := range rollupRows(t, conn, only.SQL()) {
				if !slices.Contains(tc.only, r.base) && !slices.Contains(tc.only, r.quote) {
					t.Errorf("only kept %s/%s", r.base, r.quote)
				}
			}
		})
	}
}

func readDates(t *testing.T, conn *sql.DB, r Rollup, start, end time.Time) []string {
	t.Helper()
	rows, ok := read(t, conn, r, day(start), day(end))
	if !ok {
		t.Fatalf("%s: not materialized", r.Table)
	}
	var out []string
	for _, row := range rows {
		d := row[:10]
		if !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out
}

func TestGroupedReadIncludesPartialFirstBucket(t *testing.T) {
	conn := fixtures.New(t)
	end := fixtures.LatestDate()
	for _, tc := range []struct {
		r        Rollup
		lookback int
		min      int
	}{{Monthly, 40, 2}, {Weekly, 20, 3}} {
		rebuild(t, conn, tc.r)
		if got := readDates(t, conn, tc.r, end.AddDate(0, 0, -tc.lookback), end); len(got) < tc.min {
			t.Errorf("%s: buckets %v, want at least %d", tc.r.Table, got, tc.min)
		}
	}
}

func TestGroupedReadSingleDate(t *testing.T) {
	conn := fixtures.New(t)
	date := fixtures.LatestDate()
	for _, r := range Rollups {
		rebuild(t, conn, r)
		if got := readDates(t, conn, r, date, date); len(got) == 0 {
			t.Errorf("%s: empty", r.Table)
		}
	}
}

// Not in the Ruby spec: a range with no source buckets needs nothing
// materialized, so BlendedRollup.read returns an empty result rather than nil
// (which would send the request to the live path).
func TestGroupedReadEmptyRange(t *testing.T) {
	conn := fixtures.New(t)
	start, end := time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(1900, 2, 1, 0, 0, 0, 0, time.UTC)
	for _, r := range Rollups {
		rebuild(t, conn, r)
		rows, ok, err := r.Read(ctx, conn, start, end, today())
		if err != nil {
			t.Fatal(err)
		}
		if !ok || rows == nil || len(rows) != 0 {
			t.Errorf("%s: rows %v, ok %v", r.Table, rows, ok)
		}
	}
}
