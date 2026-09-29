// Package db opens Frankfurter's SQLite database and holds its schema.
//
// Dates are stored as YYYY-MM-DD text, as the Ruby app (Sequel) writes them. The modernc driver parses text in
// columns declared DATE into time.Time when scanning, so scan such columns into time.Time (UTC midnight), or select
// them through a function such as date(col) or max(col) to get the text back. Bind dates as YYYY-MM-DD strings (see
// FormatDate); a bound time.Time would be written in a different text format and break comparisons.
package db

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// Schema is the DDL of the migrated Ruby database.
//
//go:embed schema.sql
var Schema string

// DateLayout is how dates are stored.
const DateLayout = "2006-01-02"

// FormatDate renders t as stored in date columns.
func FormatDate(t time.Time) string { return t.Format(DateLayout) }

// ParseDate parses a stored date into UTC midnight.
func ParseDate(s string) (time.Time, error) { return time.Parse(DateLayout, s) }

// Open opens the SQLite database at path with the connection settings of the Ruby app: WAL, NORMAL sync, a 128 MB
// mmap, a capped journal, and a busy timeout (SQLITE_BUSY_TIMEOUT milliseconds, default 60000). The pool size follows
// MAX_THREADS (default 5).
func Open(path string) (*sql.DB, error) {
	busy, err := envInt("SQLITE_BUSY_TIMEOUT", 60_000)
	if err != nil {
		return nil, err
	}
	conns, err := envInt("MAX_THREADS", 5)
	if err != nil {
		return nil, err
	}

	q := url.Values{}
	for _, p := range []string{
		fmt.Sprintf("busy_timeout(%d)", busy),
		"journal_mode(WAL)",
		"synchronous(NORMAL)",
		"mmap_size(134217728)",
		"journal_size_limit(27103364)",
	} {
		q.Add("_pragma", p)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(conns)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return db, nil
}

// DefaultPath mirrors lib/db.rb: DATABASE_URL (sqlite://path) wins; otherwise db/frankfurter[_APP_ENV][_TEST_ENV_NUMBER].sqlite3
// under the working directory.
func DefaultPath() (string, error) {
	if raw := os.Getenv("DATABASE_URL"); raw != "" {
		path, ok := strings.CutPrefix(raw, "sqlite://")
		if !ok {
			return "", fmt.Errorf("frankfurter now uses SQLite: remove DATABASE_URL or set it to a sqlite URL")
		}
		return path, nil
	}
	name := "frankfurter"
	if env := os.Getenv("APP_ENV"); env != "" {
		name += "_" + env
		if worker := os.Getenv("TEST_ENV_NUMBER"); worker != "" {
			name += "_" + worker
		}
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(wd, "db", name+".sqlite3"), nil
}

// CreateSchema creates every table and index of a fresh database.
func CreateSchema(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, Schema)
	return err
}

func envInt(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}
