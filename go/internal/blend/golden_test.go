package blend

import (
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/dbtest"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// tasksGolden is what go/scripts/blend_golden.rb records: the Ruby tasks' input
// rates, the tables after each stage, and the consensus scan of the input.
type tasksGolden struct {
	Today     string         `json:"today"`
	Rates     [][]any        `json:"rates"`
	Future    [][]any        `json:"future"` // inserted before the purge stage
	Leads     map[string]int `json:"leads"`
	PurgeLine string         `json:"purge_line"`
	Consensus struct {
		TotalLine string         `json:"total_line"`
		Counts    map[string]int `json:"counts"`
		Dates     int            `json:"dates"`
	} `json:"consensus"`

	Reads map[string][]goldenRead `json:"reads"` // BlendedRollup.read after blend:rebuild, by table

	Rollups map[string][][]any `json:"rollups"`
	Blend   map[string][][]any `json:"blend"`
	ECB     map[string][][]any `json:"ecb"`
	Purge   map[string][][]any `json:"purge"`
}

// goldenRead is one BlendedRollup.read: rows nil when Ruby returned nil (fall
// back to live). With Dropped set, that bucket was deleted first inside a
// rolled-back transaction.
type goldenRead struct {
	From    string  `json:"from"`
	To      string  `json:"to"`
	Dropped string  `json:"dropped"`
	Rows    [][]any `json:"rows"`
}

func loadTasksGolden(t *testing.T) tasksGolden {
	t.Helper()
	f, err := os.Open("testdata/golden/tasks.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var g tasksGolden
	if err := json.NewDecoder(zr).Decode(&g); err != nil {
		t.Fatal(err)
	}
	return g
}

func insertGoldenRates(t *testing.T, conn *sql.DB, rows [][]any) {
	t.Helper()
	if err := db.Immediate(ctx, conn, func(q db.Querier) error {
		for _, r := range rows {
			if _, err := q.ExecContext(ctx, "INSERT INTO rates (date, provider, base, quote, mid, bid, ask) VALUES "+
				"(?, ?, ?, ?, ?, ?, ?)", r...); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// dumpTable reads table as rows of text keys and a trailing rate, ordered like
// the Ruby dump.
func dumpTable(t *testing.T, q db.Querier, table string) [][]any {
	t.Helper()
	cols := "bucket_date, provider, base, quote, rate"
	switch {
	case table == "rates":
		cols = "date, provider, base, quote, rate"
	case table == "blended_rates":
		cols = "date, quote, rate"
	case strings.HasPrefix(table, "blended_"):
		cols = "bucket_date, quote, rate"
	}
	rows, err := q.QueryContext(ctx, "SELECT "+cols+" FROM "+table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := strings.Count(cols, ",") + 1
	var out [][]any
	for rows.Next() {
		var d db.NullDate
		keys := make([]string, n-2)
		var rate float64
		dest := []any{&d}
		for i := range keys {
			dest = append(dest, &keys[i])
		}
		dest = append(dest, &rate)
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		row := []any{db.FormatDate(d.Time)}
		for _, k := range keys {
			row = append(row, k)
		}
		out = append(out, append(row, rate))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// compareTable pairs rows on every column but the last (the rate) and compares
// rates within 1e-9 relative.
func compareTable(t *testing.T, label string, want, got [][]any) {
	t.Helper()
	index := func(rows [][]any) map[string]float64 {
		m := make(map[string]float64, len(rows))
		for _, r := range rows {
			m[fmt.Sprint(r[:len(r)-1]...)] = r[len(r)-1].(float64)
		}
		return m
	}
	w, g := index(want), index(got)
	var problems []string
	for k, wv := range w {
		gv, ok := g[k]
		switch {
		case !ok:
			problems = append(problems, "missing "+k)
		case math.Abs(gv-wv) > 1e-9*math.Max(math.Abs(wv), math.Abs(gv)):
			problems = append(problems, fmt.Sprintf("differs %s: ruby %v, go %v", k, wv, gv))
		}
	}
	for k := range g {
		if _, ok := w[k]; !ok {
			problems = append(problems, "extra "+k)
		}
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Errorf("%s: %d of %d rows mismatch; first: %s", label, len(problems), len(want),
			strings.Join(problems[:min(10, len(problems))], "; "))
	}
}

func compareStage(t *testing.T, q db.Querier, stage string, want map[string][][]any) {
	t.Helper()
	if len(want) == 0 {
		t.Fatalf("%s: stage missing from golden", stage)
	}
	for table, rows := range want {
		if len(rows) == 0 {
			t.Errorf("%s/%s: empty golden table", stage, table)
		}
		compareTable(t, stage+"/"+table, rows, dumpTable(t, q, table))
	}
}

// TestGoldenTasks replays go/scripts/blend_golden.rb: the consensus scan,
// rollups:rebuild, blend:rebuild, rollups:rebuild[ecb] after ECB's GBP rates
// move, then db:purge_invalid after rows beyond the future horizon are inserted
// and rolled up. It compares every table Ruby produced at each stage.
func TestGoldenTasks(t *testing.T) {
	g := loadTasksGolden(t)
	today, err := db.ParseDate(g.Today)
	if err != nil {
		t.Fatal(err)
	}
	conn := dbtest.New(t)
	if err := rates.SeedProviders(ctx, conn); err != nil {
		t.Fatal(err)
	}
	insertGoldenRates(t, conn, g.Rates)

	report, err := ScanConsensus(ctx, conn, time.Time{}, time.Time{}, today)
	if err != nil {
		t.Fatal(err)
	}
	if line := fmt.Sprintf("Total: %d outliers across %d dates", report.Total, report.Dates); line != g.Consensus.TotalLine {
		t.Errorf("consensus: %q, ruby %q", line, g.Consensus.TotalLine)
	}
	counts := map[string]int{}
	for _, c := range report.Counts {
		counts[c.Provider+" "+c.Quote] = c.Count
	}
	if len(g.Consensus.Counts) == 0 || !maps.Equal(counts, g.Consensus.Counts) || report.Dates != g.Consensus.Dates {
		t.Errorf("consensus counts = %v over %d dates, ruby %v over %d", counts, report.Dates, g.Consensus.Counts,
			g.Consensus.Dates)
	}

	if err := RebuildProviderRollups(ctx, conn, "", today); err != nil {
		t.Fatal(err)
	}
	compareStage(t, conn, "rollups", g.Rollups)

	if err := RebuildAll(ctx, conn, today); err != nil {
		t.Fatal(err)
	}
	compareStage(t, conn, "blend", g.Blend)
	compareReads(t, conn, g.Reads, today)

	exec(t, conn, "UPDATE rates SET mid = mid * 1.01 WHERE provider = 'ECB' AND quote = 'GBP'")
	if err := RebuildProviderRollups(ctx, conn, "ecb", today); err != nil {
		t.Fatal(err)
	}
	compareStage(t, conn, "ecb", g.ECB)

	// Ruby's leads come from its adapters; this package registers none, so take
	// them from the golden file.
	insertGoldenRates(t, conn, g.Future)
	if err := RebuildProviderRollups(ctx, conn, "", today); err != nil {
		t.Fatal(err)
	}
	totals, err := PurgeInvalid(ctx, conn, today, g.Leads)
	if err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf("purge_invalid: deleted %d rates, %d weekly, %d monthly", totals.Rates, totals.Weekly,
		totals.Monthly)
	if line != g.PurgeLine || totals.Rates == 0 {
		t.Errorf("purge: %q, ruby %q", line, g.PurgeLine)
	}
	compareStage(t, conn, "purge", g.Purge)
}

// compareReads replays each recorded BlendedRollup.read through Rollup.Read:
// same fallback verdict, same rows in the same order, rates within 1e-9
// relative.
func compareReads(t *testing.T, conn *sql.DB, reads map[string][]goldenRead, today time.Time) {
	t.Helper()
	for _, r := range Rollups {
		if len(reads[r.Table]) == 0 {
			t.Fatalf("%s: no reads in golden", r.Table)
		}
		for _, want := range reads[r.Table] {
			label := fmt.Sprintf("%s read %s..%s", r.Table, want.From, want.To)
			start, err := db.ParseDate(want.From)
			if err != nil {
				t.Fatal(err)
			}
			end, err := db.ParseDate(want.To)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := conn.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if want.Dropped != "" {
				label += " without " + want.Dropped
				exec(t, tx, "DELETE FROM "+r.Table+" WHERE bucket_date = ?", want.Dropped)
			}
			rows, ok, err := r.Read(ctx, tx, start, end, today)
			tx.Rollback()
			if err != nil {
				t.Fatal(err)
			}
			if ok != (want.Rows != nil) {
				t.Errorf("%s: ok = %v, ruby rows %v", label, ok, want.Rows != nil)
				continue
			}
			if len(rows) != len(want.Rows) {
				t.Errorf("%s: %d rows, ruby %d", label, len(rows), len(want.Rows))
				continue
			}
			for i, w := range want.Rows {
				g := rows[i]
				wr := w[3].(float64)
				if day(g.Date) != w[0] || g.Base != w[1] || g.Quote != w[2] ||
					math.Abs(g.Rate-wr) > 1e-9*math.Max(math.Abs(wr), math.Abs(g.Rate)) {
					t.Errorf("%s: row %d = %s %s %s %v, ruby %v", label, i, day(g.Date), g.Base, g.Quote, g.Rate, w)
					break
				}
			}
		}
	}
}
