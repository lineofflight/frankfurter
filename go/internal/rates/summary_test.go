package rates_test

import (
	"database/sql"
	"reflect"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// The Ruby spec runs every case through the provider path
// (Provider#refresh_currency_summaries, scoped to ECB) and the purge path
// (RateValidation.rebuild_summaries, unscoped).
var summaryPaths = []struct{ name, provider string }{{"provider", "ECB"}, {"purge", ""}}

func emptySummaries(t *testing.T) *sql.DB {
	t.Helper()
	conn := fixtures.New(t)
	for _, table := range []string{"rates", "currencies", "currency_coverages", "currency_exclusions"} {
		exec(t, conn, "DELETE FROM "+table)
	}
	return conn
}

func endDate(t *testing.T, conn *sql.DB, table, code string) string {
	t.Helper()
	var s string
	if err := conn.QueryRow("SELECT date(end_date) FROM "+table+" WHERE iso_code = ?", code).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func refresh(t *testing.T, conn *sql.DB, provider string, codes ...string) {
	t.Helper()
	if err := rates.RefreshSummaries(ctx, conn, codes, provider); err != nil {
		t.Fatal(err)
	}
}

func TestSummaries(t *testing.T) {
	for _, path := range summaryPaths {
		t.Run(path.name, func(t *testing.T) {
			t.Run("keeps unknown codes out of both catalogues without extending their counterpart's coverage", func(t *testing.T) {
				conn := emptySummaries(t)
				exec(t, conn, `INSERT INTO rates (provider, date, base, quote, mid) VALUES
					('ECB', '2000-01-03', 'USD', 'EUR', 0.9), ('ECB', '2000-01-04', 'USD', 'SDR', 3.0),
					('ECB', '2000-01-05', 'SDR', 'USD', 0.3)`)
				refresh(t, conn, path.provider, "USD", "EUR", "SDR")

				if n := countWhere(t, conn, "currencies", "iso_code = 'SDR'"); n != 0 {
					t.Errorf("currencies SDR = %d", n)
				}
				if n := countWhere(t, conn, "currency_coverages", "iso_code = 'SDR'"); n != 0 {
					t.Errorf("coverages SDR = %d", n)
				}
				if got := endDate(t, conn, "currencies", "USD"); got != "2000-01-03" {
					t.Errorf("currencies USD end = %s", got)
				}
				if got := endDate(t, conn, "currency_coverages", "USD"); got != "2000-01-03" {
					t.Errorf("coverages USD end = %s", got)
				}
			})

			t.Run("stores the unknown code's publication dates separately", func(t *testing.T) {
				conn := emptySummaries(t)
				exec(t, conn, `INSERT INTO rates (provider, date, base, quote, mid) VALUES
					('ECB', '2000-01-04', 'USD', 'SDR', 3.0), ('ECB', '2000-01-05', 'SDR', 'USD', 0.3)`)
				refresh(t, conn, path.provider, "SDR", "USD")

				var start, end string
				if err := conn.QueryRow(`SELECT date(start_date), date(end_date) FROM currency_exclusions
					WHERE provider_key = 'ECB' AND iso_code = 'SDR'`).Scan(&start, &end); err != nil {
					t.Fatal(err)
				}
				if start != "2000-01-04" || end != "2000-01-05" {
					t.Errorf("exclusion %s..%s", start, end)
				}
				got := providersAt(t, conn, "SELECT iso_code FROM currency_exclusions WHERE provider_key = 'ECB'")
				if !reflect.DeepEqual(got, []string{"SDR"}) {
					t.Errorf("ECB exclusions = %v", got)
				}
			})

			t.Run("removes exclusions once the currency can be named", func(t *testing.T) {
				conn := emptySummaries(t)
				exec(t, conn, "INSERT INTO rates (provider, date, base, quote, mid) VALUES ('ECB', '2000-01-04', 'USD', 'EUR', 0.9)")
				exec(t, conn, `INSERT INTO currency_exclusions (provider_key, iso_code, start_date, end_date)
					VALUES ('ECB', 'EUR', '2000-01-04', '2000-01-04')`)
				refresh(t, conn, path.provider, "EUR")

				if n := countWhere(t, conn, "currency_exclusions", "iso_code = 'EUR'"); n != 0 {
					t.Errorf("exclusions = %d", n)
				}
				if n := countWhere(t, conn, "currencies", "iso_code = 'EUR'"); n != 1 {
					t.Errorf("currencies = %d", n)
				}
			})

			t.Run("keeps terminal dates exclusive without advertising post-terminal counterparts", func(t *testing.T) {
				conn := emptySummaries(t)
				exec(t, conn, `INSERT INTO rates (provider, date, base, quote, mid) VALUES
					('ECB', '2016-06-29', 'USD', 'BYR', 20000.0), ('ECB', '2016-06-30', 'USD', 'BYR', 21000.0),
					('ECB', '2016-07-01', 'USD', 'BYR', 22000.0), ('ECB', '2017-01-01', 'BYR', 'USD', 0.00004)`)
				exec(t, conn, "INSERT INTO currencies (iso_code, start_date, end_date) VALUES ('BYR', '2016-06-29', '2017-01-01')")
				exec(t, conn, `INSERT INTO currency_coverages (provider_key, iso_code, start_date, end_date)
					VALUES ('ECB', 'BYR', '2016-06-29', '2017-01-01')`)
				refresh(t, conn, path.provider, "USD", "BYR")

				for _, code := range []string{"USD", "BYR"} {
					if got := endDate(t, conn, "currencies", code); got != "2016-06-30" {
						t.Errorf("currencies %s end = %s", code, got)
					}
					if got := endDate(t, conn, "currency_coverages", code); got != "2016-06-30" {
						t.Errorf("coverages %s end = %s", code, got)
					}
				}
			})

			t.Run("omits entirely post-terminal ranges instead of inverting their dates", func(t *testing.T) {
				conn := emptySummaries(t)
				exec(t, conn, "INSERT INTO rates (provider, date, base, quote, mid) VALUES ('ECB', '2017-01-01', 'BYR', 'USD', 0.00004)")
				refresh(t, conn, path.provider, "USD", "BYR")

				if n := countWhere(t, conn, "currencies", "1"); n != 0 {
					t.Errorf("currencies = %d", n)
				}
				if n := countWhere(t, conn, "currency_coverages", "1"); n != 0 {
					t.Errorf("coverages = %d", n)
				}
			})
		})
	}
}

func TestSeedRestoresRecognizedExclusions(t *testing.T) {
	conn := emptySummaries(t)
	exec(t, conn, "INSERT INTO rates (provider, date, base, quote, mid) VALUES ('ECB', '2000-01-04', 'USD', 'EUR', 0.9)")
	exec(t, conn, `INSERT INTO currency_exclusions (provider_key, iso_code, start_date, end_date)
		VALUES ('ECB', 'EUR', '2000-01-04', '2000-01-04')`)

	if err := rates.SeedProviders(ctx, conn); err != nil {
		t.Fatal(err)
	}

	if n := countWhere(t, conn, "currency_exclusions", "iso_code = 'EUR'"); n != 0 {
		t.Errorf("exclusions = %d", n)
	}
	if n := countWhere(t, conn, "currency_coverages", "provider_key = 'ECB' AND iso_code = 'EUR'"); n != 1 {
		t.Errorf("coverages = %d", n)
	}
	for _, code := range []string{"EUR", "USD"} {
		var start string
		if err := conn.QueryRow("SELECT date(start_date) FROM currencies WHERE iso_code = ?", code).Scan(&start); err != nil {
			t.Fatal(err)
		}
		if start != "2000-01-04" {
			t.Errorf("%s start = %s", code, start)
		}
	}
}

func TestSeedClearsModernSucreExclusions(t *testing.T) {
	conn := emptySummaries(t)
	exec(t, conn, "INSERT INTO rates (provider, date, base, quote, mid) VALUES ('CBKKW', '2026-09-17', 'ECS', 'KWD', 0.000012)")
	exec(t, conn, `INSERT INTO currency_exclusions (provider_key, iso_code, start_date, end_date)
		VALUES ('CBKKW', 'ECS', '2026-09-17', '2026-09-17')`)

	if err := rates.SeedProviders(ctx, conn); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{"currency_exclusions", "currency_coverages", "currencies"} {
		if n := countWhere(t, conn, table, "iso_code = 'ECS'"); n != 0 {
			t.Errorf("%s = %d", table, n)
		}
	}
	// Provider#unknown_currencies lists CBKKW's exclusions the Money gem cannot
	// name; ECS is named and no longer excluded.
	if !currency.Named("ECS") {
		t.Error("ECS is not named")
	}
}
