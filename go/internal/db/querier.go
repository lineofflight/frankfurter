package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Querier is what *sql.DB, *sql.Tx and *sql.Conn have in common, so domain code
// runs inside or outside a transaction.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Immediate runs fn in a BEGIN IMMEDIATE transaction on one pooled connection,
// as Sequel's transaction(mode: :immediate): the write lock is taken up front,
// so a read-then-write transaction never fails to upgrade under a concurrent
// writer. fn's error, or a panic, rolls back.
func Immediate(ctx context.Context, conn *sql.DB, fn func(Querier) error) (err error) {
	c, err := conn.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if _, err := c.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			// A fresh context: the caller's may be what failed.
			c.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	if err := fn(c); err != nil {
		return err
	}
	if _, err := c.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

// Lit renders s as an SQL string literal. Scope builders inline their constants
// (currency codes, dates) so the fragments compose as plain text.
func Lit(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// LitDate renders t as a stored-date literal.
func LitDate(t time.Time) string { return Lit(FormatDate(t)) }

// LitList renders a parenthesised list of string literals. An empty list
// renders as (NULL), so IN matches nothing; NOT IN (NULL) never holds either,
// so callers drop a NOT IN condition when the list is empty.
func LitList(values []string) string {
	if len(values) == 0 {
		return "(NULL)"
	}
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = Lit(v)
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// NullDate scans a date column whether the driver hands back time.Time
// (declared DATE columns) or text (expressions such as max(date) or a bucket).
// Valid is false for NULL.
type NullDate struct {
	Time  time.Time
	Valid bool
}

// Scan implements sql.Scanner.
func (d *NullDate) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*d = NullDate{}
	case time.Time:
		*d = NullDate{Time: time.Date(v.Year(), v.Month(), v.Day(), 0, 0, 0, 0, time.UTC), Valid: true}
	case string:
		return d.parse(v)
	case []byte:
		return d.parse(string(v))
	default:
		return fmt.Errorf("db: cannot scan %T into a date", src)
	}
	return nil
}

func (d *NullDate) parse(s string) error {
	if len(s) > len(DateLayout) {
		s = s[:len(DateLayout)]
	}
	t, err := ParseDate(s)
	if err != nil {
		return err
	}
	*d = NullDate{Time: t, Valid: true}
	return nil
}
