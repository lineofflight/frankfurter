// Package dbtest gives tests a fresh, empty Frankfurter database.
package dbtest

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/db"
)

// New returns a database with the full schema in a temporary directory, closed when the test ends. Each call gets its
// own file, so parallel tests never share state.
func New(t testing.TB) *sql.DB {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "frankfurter_test.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.CreateSchema(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	return conn
}
