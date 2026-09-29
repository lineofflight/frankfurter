package ratequery

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/blend"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
)

var ctx = context.Background()

func today() time.Time { return fixtures.Today() }

func latest() time.Time { return fixtures.LatestDate() }

func d(t time.Time) string { return db.FormatDate(t) }

// params builds Params from key, value pairs.
func params(kv ...string) Params {
	p := Params{}
	for i := 0; i+1 < len(kv); i += 2 {
		p[kv[i]] = kv[i+1]
	}
	return p
}

// newQuery is RateQuery.new(params) against conn, today pinned to the
// fixture's.
func newQuery(t *testing.T, conn *sql.DB, kv ...string) *Query {
	t.Helper()
	q, err := New(ctx, conn, params(kv...), Options{Today: today()})
	if err != nil {
		t.Fatalf("New(%v): %v", kv, err)
	}
	return q
}

func newQueryWith(t *testing.T, conn *sql.DB, opts Options, kv ...string) *Query {
	t.Helper()
	if opts.Today.IsZero() {
		opts.Today = today()
	}
	q, err := New(ctx, conn, params(kv...), opts)
	if err != nil {
		t.Fatalf("New(%v): %v", kv, err)
	}
	return q
}

// invalidQuery asserts that params fail validation and returns the error.
func invalidQuery(t *testing.T, conn *sql.DB, kv ...string) error {
	t.Helper()
	_, err := New(ctx, conn, params(kv...), Options{Today: today()})
	if !IsValidation(err) {
		t.Fatalf("New(%v) = %v, want a validation error", kv, err)
	}
	return err
}

func all(t *testing.T, q *Query) []Record {
	t.Helper()
	records, err := q.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return records
}

// query runs RateQuery.new(params).to_a.
func query(t *testing.T, conn *sql.DB, kv ...string) []Record {
	t.Helper()
	return all(t, newQuery(t, conn, kv...))
}

func find(records []Record, quote string) *Record {
	for i := range records {
		if records[i].Quote == quote {
			return &records[i]
		}
	}
	return nil
}

func exec(t *testing.T, conn *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := conn.ExecContext(ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

func insert(t *testing.T, conn *sql.DB, provider string, date time.Time, base, quote string, mid float64) {
	t.Helper()
	exec(t, conn, "INSERT INTO rates (provider, date, base, quote, mid) VALUES (?, ?, ?, ?, ?)",
		provider, d(date), base, quote, mid)
}

func rebuildDaily(t *testing.T, conn *sql.DB) {
	t.Helper()
	if err := blend.RebuildDaily(ctx, conn, today()); err != nil {
		t.Fatal(err)
	}
}

func count(t *testing.T, conn *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := conn.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func ready(t *testing.T, conn *sql.DB) bool {
	t.Helper()
	ok, err := blend.DailyReady(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func roundValue(v float64) float64 {
	n, err := round(v)
	if err != nil {
		panic(err)
	}
	return n.Value
}

func dates(records []Record) []string {
	out := make([]string, len(records))
	for i, r := range records {
		out[i] = r.Date
	}
	return out
}

func uniq(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func containsString(values []string, v string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}

func isDeadline(err error) bool {
	var e *DeadlineError
	return errors.As(err, &e)
}

func isBusy(err error) bool {
	var e BusyError
	return errors.As(err, &e)
}
