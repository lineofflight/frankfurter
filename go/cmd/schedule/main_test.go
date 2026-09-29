package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/fixtures"
)

// The dry run reads the providers table named by DATABASE_URL and prints one startup line per provider and one cron
// line per scheduled provider.
func TestDryRunReadsTheDatabase(t *testing.T) {
	conn := fixtures.New(t)
	path := filepath.Join(t.TempDir(), "copy.sqlite3")
	if _, err := conn.Exec("VACUUM INTO ?", path); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATABASE_URL", "sqlite://"+path)

	var out bytes.Buffer
	if err := run(context.Background(), &out, true); err != nil {
		t.Fatal(err)
	}
	var providers, scheduled int
	if err := conn.QueryRow("SELECT count(*), count(publish_schedule) FROM providers").Scan(&providers, &scheduled); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	startup, crons := 0, 0
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "startup: backfill["):
			startup++
		case strings.HasPrefix(l, "cron: "):
			crons++
		}
	}
	if startup != providers || crons != scheduled || len(lines) != providers+scheduled {
		t.Fatalf("startup %d/%d, cron %d/%d, lines %d", startup, providers, crons, scheduled, len(lines))
	}
}
