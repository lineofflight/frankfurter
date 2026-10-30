package rates_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

func blendable(t *testing.T, conn *sql.DB, table rates.Table) rates.Query {
	t.Helper()
	filter, err := rates.LoadBlendFilter(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	return table.Blendable(filter)
}

func pairs(t *testing.T, conn *sql.DB, q rates.Query) [][2]string {
	t.Helper()
	rows, err := conn.QueryContext(ctx, q.Columns("base, quote").SQL())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var p [2]string
		if err := rows.Scan(&p[0], &p[1]); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func count(t *testing.T, conn *sql.DB, q rates.Query) int {
	t.Helper()
	var n int
	if err := conn.QueryRowContext(ctx, q.Columns("count(*)").OrderBy("").SQL()).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// rate returns the single rate q selects, as Sequel's get(:rate).
func rate(t *testing.T, conn *sql.DB, q rates.Query) float64 {
	t.Helper()
	var r float64
	if err := conn.QueryRowContext(ctx, q.Columns("rate").SQL()+" LIMIT 1").Scan(&r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestBlendableExcludesUnknownCurrencies(t *testing.T) {
	conn := fixtures.New(t)
	exec(t, conn, `INSERT INTO rates (provider, date, base, quote, mid) VALUES
		('ECB', '2000-01-03', 'USD', 'EUR', 0.9), ('ECB', '2000-01-03', 'USD', 'SDR', 2.0),
		('ECB', '2000-01-03', 'SDR', 'EUR', 3.0)`)
	got := pairs(t, conn, blendable(t, conn, rates.Daily).Filter("date = '2000-01-03'"))
	if want := [][2]string{{"USD", "EUR"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestBlendableRecognizesAliases(t *testing.T) {
	conn := fixtures.New(t)
	for _, table := range rates.Tables {
		value := "rate"
		if table == rates.Daily {
			value = "mid"
		}
		exec(t, conn, fmt.Sprintf(`INSERT INTO %s (provider, %s, base, quote, %s) VALUES
			('ECB', '2000-01-03', 'USD', 'GHC', 3.0), ('ECB', '2000-01-03', 'GHC', 'USD', 3.0)`,
			table.Name, table.DateColumn, value))
		q := blendable(t, conn, table).Filter(table.DateColumn + " = '2000-01-03'").OrderBy("base")
		if got, want := pairs(t, conn, q), [][2]string{{"GHC", "USD"}, {"USD", "GHC"}}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v", table.Name, got)
		}
	}
}

func TestBlendableExcludesTerminalDates(t *testing.T) {
	conn := fixtures.New(t)
	for _, d := range []string{"2016-06-30", "2016-07-01", "2016-07-02"} {
		exec(t, conn, `INSERT INTO rates (provider, date, base, quote, mid) VALUES
			('ECB', ?, 'USD', 'BYR', 20000.0), ('ECB', ?, 'BYR', 'USD', 0.00005)`, d, d)
	}
	q := blendable(t, conn, rates.Daily).Filter("base = 'BYR' OR quote = 'BYR'")
	if n := count(t, conn, q); n != 2 {
		t.Errorf("count = %d", n)
	}
	got := scanDates(t, conn, q.Columns("DISTINCT date").SQL())
	if len(got) != 1 || !got[0].Equal(time.Date(2016, 6, 30, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("dates = %v", got)
	}
}

func TestBlendableKeepsFinalSucreDay(t *testing.T) {
	conn := fixtures.New(t)
	exec(t, conn, `INSERT INTO rates (provider, date, base, quote, mid) VALUES
		('ECB', '2000-09-08', 'USD', 'ECS', 25000.0), ('ECB', '2000-09-09', 'USD', 'ECS', 25000.0)`)
	got := scanDates(t, conn, blendable(t, conn, rates.Daily).Filter("quote = 'ECS'").Columns("date").SQL())
	if len(got) != 1 || !got[0].Equal(time.Date(2000, 9, 8, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("dates = %v", got)
	}
}

type boundaryCase struct {
	table            rates.Table
	code             string
	before, terminal time.Time
}

var boundaryCases = []boundaryCase{
	{rates.Weekly, "BYR", time.Date(2016, 6, 30, 0, 0, 0, 0, time.UTC), time.Date(2016, 7, 1, 0, 0, 0, 0, time.UTC)},
	{rates.Monthly, "VEF", time.Date(2018, 8, 19, 0, 0, 0, 0, time.UTC), time.Date(2018, 8, 20, 0, 0, 0, 0, time.UTC)},
}

func pairFor(side, code string) (base, quote string) {
	if side == "base" {
		return code, "USD"
	}
	return "USD", code
}

func pairFilter(base, quote, bucket string) string {
	return "base = " + db.Lit(base) + " AND quote = " + db.Lit(quote) + " AND provider = 'ECB' AND bucket_date = " +
		db.Lit(bucket)
}

// blendInputs is every blendable rollup row of a bucket: the whole input of
// that bucket's blend, so equal inputs mean an identical materialised blend.
func blendInputs(t *testing.T, conn *sql.DB, table rates.Table, bucket string) [][5]any {
	t.Helper()
	rows, err := conn.QueryContext(ctx, blendable(t, conn, table).Filter("bucket_date = "+db.Lit(bucket)).
		Columns("provider, base, quote, rate, bucket_date").OrderBy("provider, base, quote").SQL())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [][5]any
	for rows.Next() {
		var r [5]any
		if err := rows.Scan(&r[0], &r[1], &r[2], &r[3], &r[4]); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func TestBlendableRollupBoundaries(t *testing.T) {
	for _, c := range boundaryCases {
		p := string(c.table.Precision)
		bucket := db.FormatDate(rates.Bucket(c.table.Precision, c.before))
		for _, side := range []string{"base", "quote"} {
			base, quote := pairFor(side, c.code)

			t.Run(fmt.Sprintf("preserves the stored %s boundary %s average when daily observations are all eligible", p, side), func(t *testing.T) {
				conn := fixtures.New(t)
				exec(t, conn, "INSERT INTO rates (provider, date, base, quote, mid) VALUES ('ECB', ?, ?, ?, 15.123456789012)",
					db.FormatDate(c.before), base, quote)
				exec(t, conn, "INSERT INTO "+c.table.Name+" (provider, bucket_date, base, quote, rate) VALUES ('ECB', ?, ?, ?, 15.123456789011)",
					bucket, base, quote)
				if got := rate(t, conn, blendable(t, conn, c.table).Filter(pairFilter(base, quote, bucket))); got != 15.123456789011 {
					t.Errorf("rate = %v", got)
				}
			})

			t.Run(fmt.Sprintf("preserves the stored %s boundary %s average when no daily observations remain", p, side), func(t *testing.T) {
				conn := fixtures.New(t)
				exec(t, conn, "INSERT INTO "+c.table.Name+" (provider, bucket_date, base, quote, rate) VALUES ('ECB', ?, ?, ?, 15.123456789011)",
					bucket, base, quote)
				if got := rate(t, conn, blendable(t, conn, c.table).Filter(pairFilter(base, quote, bucket))); got != 15.123456789011 {
					t.Errorf("rate = %v", got)
				}
			})

			// Ruby compares the materialised blended rollup before and after;
			// that table belongs to the blending step, so this compares the
			// blend's whole input (every blendable row of the bucket) instead.
			t.Run(fmt.Sprintf("keeps %s blends identical when retained expired %s rows share a bucket", p, side), func(t *testing.T) {
				conn := fixtures.New(t)
				exec(t, conn, "INSERT INTO rates (provider, date, base, quote, mid) VALUES ('ECB', ?, ?, ?, 15.123456789012)",
					db.FormatDate(c.before), base, quote)
				if _, err := rates.RefreshRollups(ctx, conn, "ECB", []time.Time{c.before}); err != nil {
					t.Fatal(err)
				}
				original := blendInputs(t, conn, c.table, bucket)

				exec(t, conn, "INSERT INTO rates (provider, date, base, quote, mid) VALUES ('ECB', ?, ?, ?, 3000.0)",
					db.FormatDate(c.terminal), base, quote)
				exec(t, conn, "INSERT INTO rates (provider, date, base, quote, mid) VALUES ('ECB', ?, 'USD', 'SDR', 5000.0)",
					db.FormatDate(c.terminal))
				if _, err := rates.RefreshRollups(ctx, conn, "ECB", []time.Time{c.before, c.terminal}); err != nil {
					t.Fatal(err)
				}

				published := rate(t, conn, c.table.Dataset().Filter(pairFilter(base, quote, bucket)))
				if published != (15.123456789012+3000.0)/2 {
					t.Errorf("published = %v", published)
				}
				sdr := c.table.Dataset().Filter("provider = 'ECB' AND bucket_date = " + db.Lit(bucket) + " AND quote = 'SDR'")
				if n := count(t, conn, sdr); n != 1 {
					t.Errorf("SDR rollups = %d", n)
				}
				if got := blendInputs(t, conn, c.table, bucket); !reflect.DeepEqual(got, original) {
					t.Errorf("blend inputs changed:\n%v\n%v", original, got)
				}
				if got := rate(t, conn, blendable(t, conn, c.table).Filter(pairFilter(base, quote, bucket))); got != 15.123456789012 {
					t.Errorf("blended = %v", got)
				}
				if n := count(t, conn, blendable(t, conn, c.table).Filter("quote = 'SDR'")); n != 0 {
					t.Errorf("blendable SDR = %d", n)
				}
			})
		}

		t.Run(fmt.Sprintf("omits %s pairs whose bucket contains only expired observations", p), func(t *testing.T) {
			conn := fixtures.New(t)
			exec(t, conn, "INSERT INTO rates (provider, date, base, quote, mid) VALUES ('ECB', ?, 'USD', ?, 30.0)",
				db.FormatDate(c.terminal), c.code)
			if _, err := rates.RefreshRollups(ctx, conn, "ECB", []time.Time{c.terminal}); err != nil {
				t.Fatal(err)
			}
			terminalBucket := db.LitDate(rates.Bucket(c.table.Precision, c.terminal))
			cond := "bucket_date = " + terminalBucket + " AND quote = " + db.Lit(c.code)
			if n := count(t, conn, c.table.Dataset().Filter(cond)); n != 1 {
				t.Errorf("stored = %d", n)
			}
			// With no blendable input, the blended rollup has no row for the
			// pair either.
			if n := count(t, conn, blendable(t, conn, c.table).Filter(cond)); n != 0 {
				t.Errorf("blendable = %d", n)
			}
		})

		t.Run(fmt.Sprintf("uses stored %s rollups for pairs unaffected by a terminal date", p), func(t *testing.T) {
			conn := fixtures.New(t)
			exec(t, conn, "INSERT INTO "+c.table.Name+" (provider, bucket_date, base, quote, rate) VALUES ('ECB', '2000-01-01', 'USD', 'EUR', 0.876543210123)")
			if got := rate(t, conn, blendable(t, conn, c.table).Filter("bucket_date = '2000-01-01'")); got != 0.876543210123 {
				t.Errorf("rate = %v", got)
			}
		})
	}
}

func TestWeeklyCoverageUsesCoveringIndex(t *testing.T) {
	conn := fixtures.New(t)
	today := fixtures.Today()
	q := blendable(t, conn, rates.Weekly).Between(today.AddDate(0, 0, -365), today, today).
		Columns("DISTINCT bucket_date").OrderBy("bucket_date")
	rows, err := conn.QueryContext(ctx, "EXPLAIN QUERY PLAN "+q.SQL())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var searches []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(detail, "SEARCH weekly_rates") {
			searches = append(searches, detail)
		}
	}
	if len(searches) != 2 {
		t.Fatalf("searches = %q", searches)
	}
	for _, s := range searches {
		if !strings.Contains(s, "USING COVERING INDEX") {
			t.Errorf("search %q does not use a covering index", s)
		}
	}
}
