package blend

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/dbtest"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// tasksGolden is what go/scripts/blend_golden.rb records: the Ruby tasks' input rates and the tables after each stage.
type tasksGolden struct {
	Today  string
	Rates  [][]any
	Stages map[string]map[string][][]any `json:"-"`
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
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(zr).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	g := tasksGolden{Stages: map[string]map[string][][]any{}}
	if err := json.Unmarshal(raw["today"], &g.Today); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw["rates"], &g.Rates); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"rollups", "blend", "ecb"} {
		var tables map[string][][]any
		if err := json.Unmarshal(raw[stage], &tables); err != nil {
			t.Fatal(err)
		}
		g.Stages[stage] = tables
	}
	return g
}

// dumpTable reads table as rows of text keys and a trailing rate, ordered like the Ruby dump.
func dumpTable(t *testing.T, q db.Querier, table string) [][]any {
	t.Helper()
	cols := "bucket_date, provider, base, quote, rate"
	switch {
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
	return out
}

// compareTable pairs rows on every column but the last (the rate) and compares rates within 1e-9 relative.
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
	for table, rows := range want {
		if len(rows) == 0 {
			t.Errorf("%s/%s: empty golden table", stage, table)
		}
		compareTable(t, stage+"/"+table, rows, dumpTable(t, q, table))
	}
}

// TestGoldenTasks replays go/scripts/blend_golden.rb: rollups:rebuild, blend:rebuild, then rollups:rebuild[ecb]
// after ECB's GBP rates move, and compares every table Ruby produced at each stage.
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
	if err := db.Immediate(ctx, conn, func(q db.Querier) error {
		for _, r := range g.Rates {
			if _, err := q.ExecContext(ctx, "INSERT INTO rates (date, provider, base, quote, mid, bid, ask) VALUES "+
				"(?, ?, ?, ?, ?, ?, ?)", r...); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := RebuildProviderRollups(ctx, conn, "", today); err != nil {
		t.Fatal(err)
	}
	compareStage(t, conn, "rollups", g.Stages["rollups"])

	if err := RebuildAll(ctx, conn, today); err != nil {
		t.Fatal(err)
	}
	compareStage(t, conn, "blend", g.Stages["blend"])

	exec(t, conn, "UPDATE rates SET mid = mid * 1.01 WHERE provider = 'ECB' AND quote = 'GBP'")
	if err := RebuildProviderRollups(ctx, conn, "ecb", today); err != nil {
		t.Fatal(err)
	}
	compareStage(t, conn, "ecb", g.Stages["ecb"])
}
