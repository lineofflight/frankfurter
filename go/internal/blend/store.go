package blend

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// Pivot is the base every materialized blend is stored in. Requests derive other bases from it.
const Pivot = "USD"

// Test seams. Production leaves them as they are.
var (
	wrapChunk   = func(run func() error) error { return run() }             // around each chunk RebuildDaily refreshes
	beforeBatch = func(table string, buckets []string) error { return nil } // at the start of each grouped batch
	afterRead   = func() {}                                                 // between Read's coverage and value queries
)

// within runs fn in q's transaction when q is one already (a *sql.Tx, or a *sql.Conn inside BEGIN), and otherwise in
// a new BEGIN IMMEDIATE transaction, like Sequel's transaction(**(in_transaction? ? {} : { mode: :immediate })). With
// savepoint, a joined run gets a savepoint of its own, so its failure undoes only its own writes.
func within(ctx context.Context, q db.Querier, savepoint bool, fn func(db.Querier) error) error {
	if conn, ok := q.(*sql.DB); ok {
		return db.Immediate(ctx, conn, fn)
	}
	if !savepoint {
		return fn(q)
	}
	if _, err := q.ExecContext(ctx, "SAVEPOINT blend"); err != nil {
		return err
	}
	if err := fn(q); err != nil {
		q.ExecContext(context.Background(), "ROLLBACK TO blend")
		q.ExecContext(context.Background(), "RELEASE blend")
		return err
	}
	_, err := q.ExecContext(ctx, "RELEASE blend")
	return err
}

// stored is one materialized row.
type stored struct {
	date  string
	quote string
	rate  float64
}

// insert writes rows into table (with date column col) in batches of 1000.
func insert(ctx context.Context, q db.Querier, table, col string, rows []stored) error {
	for start := 0; start < len(rows); start += 1000 {
		batch := rows[start:min(start+1000, len(rows))]
		args := make([]any, 0, 3*len(batch))
		for _, r := range batch {
			args = append(args, r.date, r.quote, r.rate)
		}
		values := strings.TrimSuffix(strings.Repeat("(?, ?, ?), ", len(batch)), ", ")
		if _, err := q.ExecContext(ctx, "INSERT INTO "+table+" ("+col+", quote, rate) VALUES "+values, args...); err != nil {
			return fmt.Errorf("insert %s: %w", table, err)
		}
	}
	return nil
}

// scalarDate returns a single date (or NULL) query result.
func scalarDate(ctx context.Context, q db.Querier, query string) (db.NullDate, error) {
	var d db.NullDate
	err := q.QueryRowContext(ctx, query).Scan(&d)
	return d, err
}

// bucketDates returns the first column of query as stored date text.
func bucketDates(ctx context.Context, q db.Querier, query string) ([]string, error) {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d db.NullDate
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, db.FormatDate(d.Time))
	}
	return out, rows.Err()
}

func count(ctx context.Context, q db.Querier, table string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n)
	return n, err
}

// uniq drops repeated values, keeping first occurrences in order.
func uniq(values []string) []string {
	seen := make(map[string]bool, len(values))
	var out []string
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func blendable(ctx context.Context, q db.Querier, t rates.Table) (rates.Query, error) {
	nonBlending, err := rates.NonBlendingKeys(ctx, q)
	if err != nil {
		return rates.Query{}, err
	}
	return t.Blendable(nonBlending), nil
}

// addMonths is Ruby's Date#>>: the same day n months on, clamped to the end of a shorter month.
func addMonths(d time.Time, n int) time.Time {
	y, m, day := d.Date()
	first := time.Date(y, m+time.Month(n), 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1).Day()
	return time.Date(first.Year(), first.Month(), min(day, last), 0, 0, 0, 0, time.UTC)
}
