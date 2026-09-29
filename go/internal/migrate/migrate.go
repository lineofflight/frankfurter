// Package migrate ports db/migrate: the numbered schema and data migrations,
// applied through the same bookkeeping table as Sequel's IntegerMigrator
// (schema_info, one row holding the current version). A database the Ruby app
// has migrated opens here at its version, and one migrated here opens in Ruby
// the same way.
//
// The DDL is the SQL Sequel emitted for each migration on SQLite, so every
// version leaves the schema Ruby leaves (testdata/schemas.json records it).
// Unlike Sequel on SQLite, which runs migrations outside transactions, each
// migration and its version bump commit together in one BEGIN IMMEDIATE
// transaction: a failed migration leaves nothing behind.
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/lineofflight/frankfurter/go/internal/db"
)

// Migration is one numbered file in db/migrate. A nil Down is a no-op, as in
// Sequel.
type Migration struct {
	Version  int
	Name     string
	Up, Down func(ctx context.Context, q db.Querier) error
}

// ErrIrreversible is returned by a Down that cannot run.
var ErrIrreversible = errors.New("irreversible migration")

// Latest is the newest migration's version.
func Latest() int { return len(migrations) }

// Migrations returns every migration in version order. Callers must not modify
// it.
func Migrations() []Migration { return migrations }

// Current returns the version recorded in schema_info, or 0 when the table is
// missing or empty.
func Current(ctx context.Context, q db.Querier) (int, error) {
	var n int
	err := q.QueryRowContext(ctx,
		"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_info'").Scan(&n)
	if err != nil || n == 0 {
		return 0, err
	}
	var v sql.NullInt64
	err = q.QueryRowContext(ctx, "SELECT version FROM schema_info LIMIT 1").Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return int(v.Int64), err
}

// CheckCurrent returns an error unless the database is at the latest version
// (Sequel::Migrator.check_current).
func CheckCurrent(ctx context.Context, q db.Querier) error {
	v, err := Current(ctx, q)
	if err != nil {
		return err
	}
	if v != Latest() {
		return fmt.Errorf("migrator is not current: database at version %d, latest is %d", v, Latest())
	}
	return nil
}

// Up migrates to the latest version.
func Up(ctx context.Context, conn *sql.DB) error { return To(ctx, conn, Latest()) }

// To migrates up or down to target (rake db:migrate VERSION=target). Each
// migration commits with its version bump; the first failure stops the run,
// leaving the database at the last version that succeeded.
func To(ctx context.Context, conn *sql.DB, target int) error {
	if target < 0 || target > Latest() {
		return fmt.Errorf("migrate: no migration %d (latest is %d)", target, Latest())
	}
	if err := ensureTable(ctx, conn); err != nil {
		return err
	}
	current, err := Current(ctx, conn)
	if err != nil {
		return err
	}
	if current > Latest() {
		return fmt.Errorf("migrate: database at version %d is newer than the latest migration %d", current, Latest())
	}
	for v := current + 1; v <= target; v++ {
		if err := apply(ctx, conn, migrations[v-1], "up", v); err != nil {
			return err
		}
	}
	for v := current; v > target; v-- {
		if err := apply(ctx, conn, migrations[v-1], "down", v-1); err != nil {
			return err
		}
	}
	return nil
}

func ensureTable(ctx context.Context, conn *sql.DB) error {
	return db.Immediate(ctx, conn, func(q db.Querier) error {
		if _, err := q.ExecContext(ctx,
			"CREATE TABLE IF NOT EXISTS `schema_info` (`version` integer DEFAULT (0) NOT NULL)"); err != nil {
			return err
		}
		_, err := q.ExecContext(ctx,
			"INSERT INTO `schema_info` (`version`) SELECT 0 WHERE NOT EXISTS (SELECT 1 FROM `schema_info`)")
		return err
	})
}

func apply(ctx context.Context, conn *sql.DB, m Migration, direction string, version int) error {
	step := m.Up
	if direction == "down" {
		step = m.Down
	}
	err := db.Immediate(ctx, conn, func(q db.Querier) error {
		if step != nil {
			if err := step(ctx, q); err != nil {
				return err
			}
		}
		_, err := q.ExecContext(ctx, "UPDATE `schema_info` SET `version` = ?", version)
		return err
	})
	if err != nil {
		return fmt.Errorf("migration %03d_%s %s: %w", m.Version, m.Name, direction, err)
	}
	slog.Info("migrated", "migration", fmt.Sprintf("%03d_%s", m.Version, m.Name), "direction", direction)
	return nil
}

// exec runs statements in order.
func exec(ctx context.Context, q db.Querier, statements ...string) error {
	for _, s := range statements {
		if _, err := q.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

// sqlStep is a migration step made of plain statements.
func sqlStep(statements ...string) func(context.Context, db.Querier) error {
	return func(ctx context.Context, q db.Querier) error { return exec(ctx, q, statements...) }
}

func irreversible(context.Context, db.Querier) error { return ErrIrreversible }
