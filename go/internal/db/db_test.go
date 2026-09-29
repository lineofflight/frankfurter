package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/dbtest"
)

func TestOpenUsesWAL(t *testing.T) {
	conn := dbtest.New(t)
	var mode string
	if err := conn.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
	var timeout int
	if err := conn.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if timeout != 60_000 {
		t.Errorf("busy_timeout = %d, want 60000", timeout)
	}
}

func TestSchemaResolvesRateFromComponents(t *testing.T) {
	conn := dbtest.New(t)
	ctx := context.Background()
	_, err := conn.ExecContext(ctx, `INSERT INTO rates (date, base, quote, provider, mid, bid, ask) VALUES
		('2026-03-16', 'EUR', 'USD', 'ECB', 1.1, NULL, NULL),
		('2026-03-16', 'USD', 'IDR', 'BI', NULL, 181.5264, 181.76),
		('2026-03-16', 'USD', 'JMD', 'BOJA', NULL, 0, 157.2)`)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]float64{"ECB": 1.1, "BI": 181.6432, "BOJA": 157.2}
	rows, err := conn.QueryContext(ctx, "SELECT provider, rate FROM rates")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var provider string
		var rate float64
		if err := rows.Scan(&provider, &rate); err != nil {
			t.Fatal(err)
		}
		if rate != want[provider] {
			t.Errorf("%s rate = %v, want %v", provider, rate, want[provider])
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestDateRoundTrip(t *testing.T) {
	conn := dbtest.New(t)
	day := time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC)
	_, err := conn.Exec("INSERT INTO rates (date, base, quote, provider, mid) VALUES (?, 'EUR', 'USD', 'ECB', 1.1)",
		db.FormatDate(day))
	if err != nil {
		t.Fatal(err)
	}

	var stored string
	if err := conn.QueryRow("SELECT typeof(date) || ':' || date FROM rates").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "text:2026-03-16" {
		t.Errorf("stored %q, want text:2026-03-16", stored)
	}

	var got time.Time
	if err := conn.QueryRow("SELECT date FROM rates").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Equal(day) {
		t.Errorf("scanned %v, want %v", got, day)
	}

	var latest string
	if err := conn.QueryRow("SELECT max(date) FROM rates").Scan(&latest); err != nil {
		t.Fatal(err)
	}
	if latest != "2026-03-16" {
		t.Errorf("max(date) = %q, want 2026-03-16", latest)
	}
}
