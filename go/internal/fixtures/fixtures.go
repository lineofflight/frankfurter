// Package fixtures seeds test databases with realistic ECB, BOC and BOJ rates,
// as spec/fixtures.rb does. Every date is relative to today so tests never go
// stale.
//
// New(t) is the usual entry point: a fresh database per test, already seeded,
// the Go counterpart of spec/helper.rb's seed plus per-test rollback.
package fixtures

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// BusinessDays is how many weekdays of history the fixture holds, about two
// years: enough for downsampling and range tests.
const BusinessDays = 520

type pair struct {
	Base, Quote string
	Rate        float64
}

type providerRates struct {
	Key   string
	Pairs []pair
}

func quotes(base string, kv ...any) []pair {
	out := make([]pair, 0, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		out = append(out, pair{base, kv[i].(string), kv[i+1].(float64)})
	}
	return out
}

// baseRates is Fixtures::BASE_RATES, in the same order.
var baseRates = []providerRates{
	{"ECB", quotes("EUR", "USD", 1.08, "GBP", 0.86, "JPY", 160.0, "CAD", 1.47, "INR", 90.0, "CHF", 0.95, "SEK", 11.2,
		"NOK", 11.5, "PLN", 4.3, "CZK", 25.1, "PHP", 81.0193)},
	{"BOC", quotes("CAD", "USD", 0.74, "EUR", 0.68, "GBP", 0.58, "JPY", 109.0)},
	{"BOJ", []pair{{"EUR", "USD", 1.08}, {"USD", "JPY", 155.0}}},
}

var today = sync.OnceValue(rates.Today)

// Today is the date the fixture data is relative to, fixed at first use so a
// test run straddling midnight stays consistent.
func Today() time.Time { return today() }

var businessDays = sync.OnceValue(func() []time.Time {
	days := make([]time.Time, 0, BusinessDays)
	for d := Today(); len(days) < BusinessDays; d = d.AddDate(0, 0, -1) {
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday {
			days = append(days, d)
		}
	}
	return days
})

// LatestDate is the most recent business day in the fixture.
func LatestDate() time.Time { return businessDays()[0] }

// BusinessDay is a business day roughly daysAgo calendar days back, guaranteed
// to be in the fixture.
func BusinessDay(daysAgo int) time.Time {
	limit := Today().AddDate(0, 0, -daysAgo)
	for _, d := range businessDays() {
		if !d.After(limit) {
			return d
		}
	}
	return time.Time{}
}

// RecentSunday is the latest Sunday on or before today, for weekend snap tests.
func RecentSunday() time.Time {
	d := Today()
	for d.Weekday() != time.Sunday {
		d = d.AddDate(0, 0, -1)
	}
	return d
}

// PrecedingFriday is the latest Friday on or before date.
func PrecedingFriday(date time.Time) time.Time {
	for date.Weekday() != time.Friday {
		date = date.AddDate(0, 0, -1)
	}
	return date
}

// GapBoundaryMonday is a Monday in the fixture at least daysAgo days back
// (Ruby's default is 60): the first publication after a weekend gap, mirroring
// the production first-publish-after-a-holiday case.
func GapBoundaryMonday(daysAgo int) time.Time {
	limit := Today().AddDate(0, 0, -daysAgo)
	for _, d := range businessDays() {
		if d.Weekday() == time.Monday && !d.After(limit) {
			return d
		}
	}
	return time.Time{}
}

// Seed is Fixtures.seed!: it seeds providers, replaces all rates and blends
// with the generated history, and rebuilds the provider rollups, currencies and
// currency coverages from it.
func Seed(ctx context.Context, conn *sql.DB) error {
	if err := rates.SeedProviders(ctx, conn); err != nil {
		return err
	}
	return db.Immediate(ctx, conn, func(q db.Querier) error {
		for _, table := range []string{"rates", "blended_rates", "blended_weekly_rates", "blended_monthly_rates"} {
			if _, err := q.ExecContext(ctx, "DELETE FROM "+table); err != nil {
				return err
			}
		}
		for _, p := range baseRates {
			for _, d := range businessDays() {
				jitter := 1.0 + float64(julianDay(d)%100-50)*0.001
				for _, pr := range p.Pairs {
					if _, err := q.ExecContext(ctx,
						"INSERT INTO rates (provider, date, base, quote, mid) VALUES (?, ?, ?, ?, ?)",
						p.Key, db.FormatDate(d), pr.Base, pr.Quote, roundHalfUp(pr.Rate*jitter, 4)); err != nil {
						return err
					}
				}
			}
		}
		if err := rates.RebuildRollups(ctx, q); err != nil {
			return err
		}
		return rebuildCurrencies(ctx, q)
	})
}

func rebuildCurrencies(ctx context.Context, q db.Querier) error {
	for _, stmt := range []string{
		"DELETE FROM currencies",
		`INSERT INTO currencies (iso_code, start_date, end_date)
		SELECT iso_code, MIN(start_date), MAX(end_date)
		FROM (
			SELECT quote AS iso_code, MIN(date) AS start_date, MAX(date) AS end_date FROM rates GROUP BY quote
			UNION ALL
			SELECT base AS iso_code, MIN(date) AS start_date, MAX(date) AS end_date FROM rates GROUP BY base
		)
		GROUP BY iso_code
		ORDER BY iso_code`,
		"DELETE FROM currency_coverages",
		`INSERT INTO currency_coverages (provider_key, iso_code, start_date, end_date)
		SELECT provider, iso_code, MIN(date), MAX(date)
		FROM (
			SELECT provider, quote AS iso_code, date FROM rates
			UNION ALL
			SELECT provider, base AS iso_code, date FROM rates
		)
		GROUP BY provider, iso_code
		ORDER BY provider, iso_code`,
	} {
		if _, err := q.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// julianDay is Ruby's Date#jd.
func julianDay(d time.Time) int {
	return int(d.Unix()/86400) + 2440588
}

// roundHalfUp is Ruby's Float#round(ndigits) for small ndigits (round_half_up
// in numeric.c): round x*10^n, then correct for the scaling error so the result
// is the correctly rounded decimal.
func roundHalfUp(x float64, ndigits int) float64 {
	s := math.Pow(10, float64(ndigits))
	f := math.Round(x * s)
	if x > 0 && (f+0.5)/s <= x {
		f++
	} else if x < 0 && (f-0.5)/s >= x {
		f--
	}
	return f / s
}

var template = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "frankfurter-fixtures-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	conn, err := db.Open(filepath.Join(dir, "seed.sqlite3"))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx := context.Background()
	if err := db.CreateSchema(ctx, conn); err != nil {
		return nil, err
	}
	if err := Seed(ctx, conn); err != nil {
		return nil, fmt.Errorf("seed fixtures: %w", err)
	}
	snapshot := filepath.Join(dir, "snapshot.sqlite3")
	if _, err := conn.ExecContext(ctx, "VACUUM INTO ?", snapshot); err != nil {
		return nil, err
	}
	return os.ReadFile(snapshot)
})

// New returns a seeded database of the test's own, closed when the test ends.
// The fixture is generated once per test binary and copied, so tests may change
// it freely and run in parallel.
func New(t testing.TB) *sql.DB {
	t.Helper()
	data, err := template()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "frankfurter_test.sqlite3")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}
