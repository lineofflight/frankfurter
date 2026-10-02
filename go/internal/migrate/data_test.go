package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/db"
)

// tableDump is one table as go/scripts/migration_data.rb writes it: every row,
// ordered by every column.
type tableDump struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

func dumpTables(t *testing.T, conn *sql.DB) map[string]tableDump {
	t.Helper()
	ctx := context.Background()
	rows, err := conn.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()

	out := map[string]tableDump{}
	for _, table := range tables {
		var d tableDump
		cols, err := conn.QueryContext(ctx, "SELECT name FROM pragma_table_xinfo(?)", table)
		if err != nil {
			t.Fatal(err)
		}
		for cols.Next() {
			var c string
			if err := cols.Scan(&c); err != nil {
				t.Fatal(err)
			}
			d.Columns = append(d.Columns, c)
		}
		cols.Close()
		list := "`" + strings.Join(d.Columns, "`, `") + "`"
		data, err := conn.QueryContext(ctx, "SELECT "+list+" FROM `"+table+"` ORDER BY "+list)
		if err != nil {
			t.Fatal(err)
		}
		for data.Next() {
			values := make([]any, len(d.Columns))
			ptrs := make([]any, len(values))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err := data.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for i, v := range values {
				if tm, ok := v.(time.Time); ok {
					values[i] = db.FormatDate(tm)
				}
			}
			d.Rows = append(d.Rows, values)
		}
		data.Close()
		out[table] = d
	}
	return out
}

// sameValue compares a Go value with its JSON counterpart, numbers within 1e-9
// relative.
func sameValue(got, want any) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	w, wantNumber := want.(float64)
	var g float64
	switch v := got.(type) {
	case int64:
		g = float64(v)
	case float64:
		g = v
	case string:
		return v == want
	case []byte:
		return string(v) == want
	default:
		return fmt.Sprint(v) == fmt.Sprint(want)
	}
	if !wantNumber {
		return false
	}
	return g == w || math.Abs(g-w) <= 1e-9*math.Max(math.Abs(g), math.Abs(w))
}

func compareDumps(t *testing.T, label string, got, want map[string]tableDump) {
	t.Helper()
	for table := range want {
		if _, ok := got[table]; !ok {
			t.Errorf("%s: missing table %s", label, table)
		}
	}
	for table, g := range got {
		w, ok := want[table]
		if !ok {
			t.Errorf("%s: unexpected table %s", label, table)
			continue
		}
		if strings.Join(g.Columns, ",") != strings.Join(w.Columns, ",") {
			t.Errorf("%s: %s columns %v, want %v", label, table, g.Columns, w.Columns)
			continue
		}
		if len(g.Rows) != len(w.Rows) {
			t.Errorf("%s: %s has %d rows, want %d\ngot  %v\nwant %v", label, table, len(g.Rows), len(w.Rows), g.Rows,
				w.Rows)
			continue
		}
		for i := range g.Rows {
			for j := range g.Columns {
				if !sameValue(g.Rows[i][j], w.Rows[i][j]) {
					t.Errorf("%s: %s row %d: got %v, want %v", label, table, i, g.Rows[i], w.Rows[i])
					break
				}
			}
		}
	}
}

// The data migrations leave the rows Sequel's migrator leaves.
// testdata/data/phase_vN.sql is loaded once the database reaches version N
// (fixtures for the renames, deletions, merges, rollups, precision, relabels,
// SDR normalization and exclusions); ruby.json is Ruby's dump after migrating
// up to the latest and after rolling back to 8 (go/scripts/migration_data.rb).
func TestDataMigrationsMatchRuby(t *testing.T) {
	dir := filepath.Join("testdata", "data")
	raw, err := os.ReadFile(filepath.Join(dir, "ruby.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want struct{ Up, Down map[string]tableDump }
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	phases := map[int]string{}
	paths, _ := filepath.Glob(filepath.Join(dir, "phase_v*.sql"))
	for _, p := range paths {
		v, err := strconv.Atoi(regexp.MustCompile(`\d+`).FindString(filepath.Base(p)))
		if err != nil {
			t.Fatal(err)
		}
		phases[v] = p
	}
	if len(phases) == 0 {
		t.Fatal("no phases")
	}

	conn, _ := empty(t)
	statement := regexp.MustCompile(`(?m);\s*$`)
	for v := 1; v <= Latest(); v++ {
		migrateTo(t, conn, v)
		if phases[v] == "" {
			continue
		}
		sqlText, err := os.ReadFile(phases[v])
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range statement.Split(string(sqlText), -1) {
			if s = strings.TrimSpace(s); s != "" {
				mustExec(t, conn, s)
			}
		}
	}
	compareDumps(t, "up", dumpTables(t, conn), want.Up)

	migrateTo(t, conn, 8)
	compareDumps(t, "down to 8", dumpTables(t, conn), want.Down)
}
