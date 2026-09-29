package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/db"
)

// empty opens a new database file with no tables.
func empty(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "migration.sqlite3")
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn, path
}

// migrateTo fails the test unless To succeeds.
func migrateTo(t *testing.T, conn *sql.DB, target int) {
	t.Helper()
	if err := To(context.Background(), conn, target); err != nil {
		t.Fatal(err)
	}
}

func version(t *testing.T, q db.Querier) int {
	t.Helper()
	v, err := Current(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

type master struct {
	Type    string  `json:"type"`
	Name    string  `json:"name"`
	TblName string  `json:"tbl_name"`
	SQL     *string `json:"sql"`
}

func schema(t *testing.T, q db.Querier) []master {
	t.Helper()
	rows, err := q.QueryContext(context.Background(), "SELECT type, name, tbl_name, sql FROM sqlite_master ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []master{}
	for rows.Next() {
		var m master
		var s sql.NullString
		if err := rows.Scan(&m.Type, &m.Name, &m.TblName, &s); err != nil {
			t.Fatal(err)
		}
		if s.Valid {
			m.SQL = &s.String
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func diffSchema(t *testing.T, label string, got, want []master) {
	t.Helper()
	if reflect.DeepEqual(got, want) {
		return
	}
	render := func(ms []master) string {
		var b strings.Builder
		for _, m := range ms {
			s := "<nil>"
			if m.SQL != nil {
				s = *m.SQL
			}
			fmt.Fprintf(&b, "  %s %s on %s: %s\n", m.Type, m.Name, m.TblName, s)
		}
		return b.String()
	}
	t.Fatalf("%s: schema differs from Ruby's\ngot:\n%swant:\n%s", label, render(got), render(want))
}

// Every Ruby migration file has a Go counterpart with the same number and name.
func TestMigrationsMirrorDbMigrate(t *testing.T) {
	entries, err := os.ReadDir("../../../db/migrate")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != Latest() {
		t.Fatalf("%d Ruby migrations, %d Go", len(entries), Latest())
	}
	for i, e := range entries {
		m := Migrations()[i]
		if m.Version != i+1 {
			t.Errorf("migration %d has version %d", i+1, m.Version)
		}
		if want := fmt.Sprintf("%03d_%s.rb", m.Version, m.Name); e.Name() != want {
			t.Errorf("Ruby file %s, Go migration %s", e.Name(), want)
		}
	}
}

// Migrating up one version at a time, then down until the first irreversible migration, leaves exactly the schema
// Sequel leaves at each version (testdata/schemas.json, recorded by go/scripts/migration_schemas.rb).
func TestSchemaMatchesRubyAtEveryVersion(t *testing.T) {
	data, err := os.ReadFile("testdata/schemas.json")
	if err != nil {
		t.Fatal(err)
	}
	var recorded struct {
		Latest int                        `json:"latest"`
		Up     map[string][]master        `json:"up"`
		Down   map[string]json.RawMessage `json:"down"`
	}
	if err := json.Unmarshal(data, &recorded); err != nil {
		t.Fatal(err)
	}
	if recorded.Latest != Latest() {
		t.Fatalf("recorded latest %d, Go latest %d", recorded.Latest, Latest())
	}

	conn, _ := empty(t)
	for v := 1; v <= Latest(); v++ {
		migrateTo(t, conn, v)
		diffSchema(t, fmt.Sprintf("up to %d", v), schema(t, conn), recorded.Up[strconv.Itoa(v)])
	}

	var stop struct {
		Version int    `json:"version"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(recorded.Down["irreversible"], &stop); err != nil {
		t.Fatal(err)
	}
	for v := Latest() - 1; v >= stop.Version; v-- {
		migrateTo(t, conn, v)
		var want []master
		if err := json.Unmarshal(recorded.Down[strconv.Itoa(v)], &want); err != nil {
			t.Fatal(err)
		}
		diffSchema(t, fmt.Sprintf("down to %d", v), schema(t, conn), want)
	}

	err = To(context.Background(), conn, stop.Version-1)
	if !errors.Is(err, ErrIrreversible) || !strings.Contains(err.Error(), stop.Message) {
		t.Fatalf("down past %d: got %v, want %q", stop.Version, err, stop.Message)
	}
	if v := version(t, conn); v != stop.Version {
		t.Fatalf("version %d after the failed rollback, want %d", v, stop.Version)
	}
}

// A migrated database has the schema internal/db embeds (dumped from the migrated Ruby database), and a database
// created from that schema is current, so the migrator leaves it alone.
func TestLatestMatchesEmbeddedSchema(t *testing.T) {
	ctx := context.Background()
	migrated, _ := empty(t)
	if err := Up(ctx, migrated); err != nil {
		t.Fatal(err)
	}
	created, _ := empty(t)
	if err := db.CreateSchema(ctx, created); err != nil {
		t.Fatal(err)
	}
	diffSchema(t, "embedded", schema(t, migrated), schema(t, created))

	if err := CheckCurrent(ctx, created); err != nil {
		t.Fatal(err)
	}
	if err := Up(ctx, created); err != nil {
		t.Fatal(err)
	}
	if v := version(t, created); v != Latest() {
		t.Fatalf("version %d", v)
	}
}

// Below the irreversible merge, the first migrations roll back to an empty database.
func TestEarlyMigrationsRollBackToEmpty(t *testing.T) {
	conn, _ := empty(t)
	migrateTo(t, conn, 7)
	migrateTo(t, conn, 0)
	got := schema(t, conn)
	if len(got) != 1 || got[0].Name != "schema_info" {
		t.Fatalf("left %+v", got)
	}
	if v := version(t, conn); v != 0 {
		t.Fatalf("version %d", v)
	}
}

func TestCheckCurrentRejectsAnOutdatedDatabase(t *testing.T) {
	conn, _ := empty(t)
	if err := CheckCurrent(context.Background(), conn); err == nil {
		t.Fatal("empty database reported current")
	}
	migrateTo(t, conn, 39)
	if err := CheckCurrent(context.Background(), conn); err == nil {
		t.Fatal("version 39 reported current")
	}
	if err := To(context.Background(), conn, Latest()+1); err == nil {
		t.Fatal("accepted a target past the latest migration")
	}
}
