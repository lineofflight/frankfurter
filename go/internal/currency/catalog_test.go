package currency_test

import (
	"context"
	"database/sql"
	"slices"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
)

var ctx = context.Background()

// setup mirrors currency_spec's before block: four rates, and currencies and coverages derived from them.
func setup(t *testing.T) *sql.DB {
	t.Helper()
	conn := fixtures.New(t)
	today := fixtures.Today()
	exec(t, conn, "DELETE FROM rates")
	exec(t, conn, `INSERT INTO rates (provider, date, base, quote, mid) VALUES
		('ECB', ?, 'EUR', 'USD', 1.1), ('ECB', ?, 'EUR', 'GBP', 0.85), ('BOC', ?, 'CAD', 'USD', 0.74),
		('ECB', ?, 'EUR', 'SEK', 11.0)`,
		db.FormatDate(today), db.FormatDate(today), db.FormatDate(today), db.FormatDate(today.AddDate(0, 0, -365)))
	exec(t, conn, "DELETE FROM currencies")
	exec(t, conn, `INSERT OR REPLACE INTO currencies (iso_code, start_date, end_date)
		SELECT iso_code, MIN(start_date), MAX(end_date) FROM (
			SELECT quote AS iso_code, MIN(date) AS start_date, MAX(date) AS end_date FROM rates GROUP BY quote
			UNION ALL
			SELECT base AS iso_code, MIN(date) AS start_date, MAX(date) AS end_date FROM rates GROUP BY base
		) GROUP BY iso_code ORDER BY iso_code`)
	exec(t, conn, "DELETE FROM currency_coverages")
	exec(t, conn, `INSERT OR REPLACE INTO currency_coverages (provider_key, iso_code, start_date, end_date)
		SELECT provider, iso_code, MIN(date), MAX(date) FROM (
			SELECT provider, quote AS iso_code, date FROM rates
			UNION ALL
			SELECT provider, base AS iso_code, date FROM rates
		) GROUP BY provider, iso_code ORDER BY provider, iso_code`)
	return conn
}

func exec(t *testing.T, conn *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := conn.ExecContext(ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

func codes(list []currency.Currency) []string {
	out := make([]string, len(list))
	for i, c := range list {
		out[i] = c.ISOCode
	}
	return out
}

func mustFind(t *testing.T, conn *sql.DB, code string) *currency.Currency {
	t.Helper()
	c, err := currency.FindCurrency(ctx, conn, code)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustAll(t *testing.T, conn *sql.DB) []currency.Currency {
	t.Helper()
	all, err := currency.All(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	return all
}

func pick(list []currency.Currency, code string) *currency.Currency {
	for i := range list {
		if list[i].ISOCode == code {
			return &list[i]
		}
	}
	return nil
}

func date(s string) time.Time {
	d, err := db.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}

func TestListsAllCurrencies(t *testing.T) {
	all := codes(mustAll(t, setup(t)))
	for _, code := range []string{"USD", "EUR", "CAD"} {
		if !slices.Contains(all, code) {
			t.Errorf("missing %s", code)
		}
	}
}

func TestMergesDateRangesAcrossQuoteAndBase(t *testing.T) {
	usd := mustFind(t, setup(t), "USD")
	if usd == nil || !usd.StartDate.Equal(fixtures.Today()) || !usd.EndDate.Equal(fixtures.Today()) {
		t.Errorf("USD = %+v", usd)
	}
}

func TestIncludesBaseCurrencies(t *testing.T) {
	if mustFind(t, setup(t), "EUR") == nil {
		t.Error("EUR missing")
	}
}

func TestFiltersActiveCurrencies(t *testing.T) {
	active, err := currency.Active(ctx, setup(t), fixtures.Today())
	if err != nil {
		t.Fatal(err)
	}
	if list := codes(active); !slices.Contains(list, "USD") || slices.Contains(list, "SEK") {
		t.Errorf("active = %v", list)
	}
}

func TestReturnsNilForUnknownCurrency(t *testing.T) {
	if c := mustFind(t, setup(t), "XYZ"); c != nil {
		t.Errorf("XYZ = %+v", c)
	}
}

func TestFormatsMetadata(t *testing.T) {
	usd := mustFind(t, setup(t), "USD")
	info, _ := usd.Metadata()
	if usd.Name() != "United States Dollar" || info.Symbol != "$" || info.ISONumeric != "840" {
		t.Errorf("USD metadata = %+v", info)
	}
}

func TestIncludesProviders(t *testing.T) {
	providers, err := currency.Providers(ctx, setup(t), "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(providers, []string{"BOC", "ECB"}) {
		t.Errorf("providers = %v", providers)
	}
}

func TestFindIsCaseInsensitive(t *testing.T) {
	if mustFind(t, setup(t), "usd") == nil {
		t.Error("usd not found")
	}
}

func TestFiltersByProviders(t *testing.T) {
	list, err := currency.WithProviders(ctx, setup(t), []string{"ECB"})
	if err != nil {
		t.Fatal(err)
	}
	if got := codes(list); !slices.Contains(got, "USD") || !slices.Contains(got, "EUR") || slices.Contains(got, "CAD") {
		t.Errorf("codes = %v", got)
	}
}

func TestIncludesProviderOnlyHistoricalCurrency(t *testing.T) {
	conn := setup(t)
	exec(t, conn, `INSERT INTO currency_coverages (provider_key, iso_code, start_date, end_date)
		VALUES ('INFOREURO', 'ADP', '1994-03-01', '1998-01-01')`)

	list, err := currency.WithProviders(ctx, conn, []string{"INFOREURO"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) == 0 || list[0].ISOCode != "ADP" {
		t.Fatalf("list = %+v", list)
	}
	adp := list[0]
	if adp.Name() != "Andorran Peseta" || !adp.StartDate.Equal(date("1994-03-01")) || !adp.EndDate.Equal(date("1998-01-01")) {
		t.Errorf("ADP = %+v", adp)
	}
	if mustFind(t, conn, "ADP") != nil {
		t.Error("ADP in global find")
	}
	if slices.Contains(codes(mustAll(t, conn)), "ADP") {
		t.Error("ADP in all")
	}
	active, _ := currency.Active(ctx, conn, fixtures.Today())
	if slices.Contains(codes(active), "ADP") {
		t.Error("ADP in active")
	}
}

func TestUsesSelectedProviderDates(t *testing.T) {
	conn := setup(t)
	exec(t, conn, `INSERT INTO currency_coverages (provider_key, iso_code, start_date, end_date)
		VALUES ('INFOREURO', 'USD', '1994-03-01', '1998-01-01')`)

	list, err := currency.WithProviders(ctx, conn, []string{"INFOREURO"})
	if err != nil {
		t.Fatal(err)
	}
	if !list[0].StartDate.Equal(date("1994-03-01")) || !list[0].EndDate.Equal(date("1998-01-01")) {
		t.Errorf("USD = %+v", list[0])
	}
	if usd := mustFind(t, conn, "USD"); !usd.StartDate.Equal(fixtures.Today()) {
		t.Errorf("global USD = %+v", usd)
	}
}

func TestMergesDatesOnlyAcrossSelectedProviders(t *testing.T) {
	conn := setup(t)
	exec(t, conn, "DELETE FROM currency_coverages WHERE iso_code = 'USD'")
	exec(t, conn, `INSERT INTO currency_coverages (provider_key, iso_code, start_date, end_date) VALUES
		('INFOREURO', 'USD', '1994-03-01', '1998-01-01'),
		('ECB', 'USD', '1999-01-04', '2001-01-01'),
		('BOC', 'USD', '1990-01-01', '2010-01-01')`)

	list, err := currency.WithProviders(ctx, conn, []string{"INFOREURO", "ECB", "INFOREURO"})
	if err != nil {
		t.Fatal(err)
	}
	var usd []currency.Currency
	for _, c := range list {
		if c.ISOCode == "USD" {
			usd = append(usd, c)
		}
	}
	if len(usd) != 1 || !usd[0].StartDate.Equal(date("1994-03-01")) || !usd[0].EndDate.Equal(date("2001-01-01")) {
		t.Errorf("USD = %+v", usd)
	}
}

func TestExcludesPeggedCurrenciesWhenFilteringByProviders(t *testing.T) {
	list, err := currency.WithProviders(ctx, setup(t), []string{"ECB"})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(codes(list), "BMD") {
		t.Error("BMD listed")
	}
}

func TestIncludesPeggedCurrenciesInList(t *testing.T) {
	all := codes(mustAll(t, setup(t)))
	if !slices.Contains(all, "BMD") || !slices.Contains(all, "FKP") {
		t.Errorf("all = %v", all)
	}
}

func TestDerivesPeggedRangeFromAnchor(t *testing.T) {
	conn := setup(t)
	bmd := mustFind(t, conn, "BMD")
	if bmd == nil {
		t.Fatal("BMD missing")
	}
	if usd := mustFind(t, conn, "USD"); !bmd.EndDate.Equal(usd.EndDate) {
		t.Errorf("BMD end %v, USD end %v", bmd.EndDate, usd.EndDate)
	}
}

func TestUsesLaterOfPegSinceAndAnchorStart(t *testing.T) {
	conn := setup(t)
	bmd, usd := mustFind(t, conn, "BMD"), mustFind(t, conn, "USD")
	if bmd.StartDate.Before(usd.StartDate) {
		t.Errorf("BMD start %v before USD start %v", bmd.StartDate, usd.StartDate)
	}
}

// aedSetup is the shared arrange step of the two AED specs: AED pegged to USD since 1997-11-02, provider data only
// today, but USD history back to 1990.
func aedSetup(t *testing.T) *sql.DB {
	conn := setup(t)
	today := db.FormatDate(fixtures.Today())
	exec(t, conn, `INSERT INTO rates (provider, date, base, quote, mid) VALUES
		('ECB', '1990-01-02', 'EUR', 'USD', 1.0), ('TCMB', ?, 'USD', 'AED', 3.6725)`, today)
	exec(t, conn, "INSERT OR REPLACE INTO currencies (iso_code, start_date, end_date) VALUES ('USD', '1990-01-02', ?)", today)
	exec(t, conn, "INSERT OR REPLACE INTO currencies (iso_code, start_date, end_date) VALUES ('AED', ?, ?)", today, today)
	exec(t, conn, `INSERT OR REPLACE INTO currency_coverages (provider_key, iso_code, start_date, end_date)
		VALUES ('TCMB', 'AED', ?, ?)`, today, today)
	return conn
}

func TestExtendsStartBackToPegStart(t *testing.T) {
	conn := aedSetup(t)
	aed := mustFind(t, conn, "AED")
	if !aed.StartDate.Equal(date("1997-11-02")) || aed.Peg == nil {
		t.Errorf("AED = %+v", aed)
	}
	providers, _ := currency.Providers(ctx, conn, "AED")
	if !slices.Contains(providers, "TCMB") {
		t.Errorf("providers = %v", providers)
	}
}

func TestExtendsStartInListForPeggedCurrencies(t *testing.T) {
	aed := pick(mustAll(t, aedSetup(t)), "AED")
	if !aed.StartDate.Equal(date("1997-11-02")) {
		t.Errorf("AED = %+v", aed)
	}
}

func TestExtendsEndForPeggedCurrencyWithStaleData(t *testing.T) {
	conn := setup(t)
	exec(t, conn, "INSERT OR REPLACE INTO currencies (iso_code, start_date, end_date) VALUES ('ANG', '1999-01-04', '2025-03-28')")
	exec(t, conn, `INSERT OR REPLACE INTO currency_coverages (provider_key, iso_code, start_date, end_date)
		VALUES ('BDI', 'ANG', '1999-01-04', '2025-03-28')`)
	usd := mustFind(t, conn, "USD")

	if ang := mustFind(t, conn, "ANG"); !ang.EndDate.Equal(usd.EndDate) {
		t.Errorf("find: ANG end %v", ang.EndDate)
	}
	if ang := pick(mustAll(t, conn), "ANG"); !ang.EndDate.Equal(usd.EndDate) {
		t.Errorf("all: ANG end %v", ang.EndDate)
	}
	active, err := currency.Active(ctx, conn, fixtures.Today())
	if err != nil {
		t.Fatal(err)
	}
	if ang := pick(active, "ANG"); ang == nil || !ang.EndDate.Equal(usd.EndDate) {
		t.Errorf("active: ANG = %+v", ang)
	}
}

func TestFormatsPeggedCurrency(t *testing.T) {
	bmd := mustFind(t, setup(t), "BMD")
	if bmd.ISOCode != "BMD" || bmd.Name() != "Bermudian Dollar" || bmd.StartDate.IsZero() || bmd.EndDate.IsZero() {
		t.Errorf("BMD = %+v", bmd)
	}
}

func TestReturnsPegMetadata(t *testing.T) {
	bmd := mustFind(t, setup(t), "BMD")
	if bmd.Peg == nil || bmd.Peg.Base != "USD" || bmd.Peg.Rate != 1.0 || bmd.Peg.Authority != "Bermuda Monetary Authority" {
		t.Errorf("peg = %+v", bmd.Peg)
	}
}

func TestReturnsProvidersForPeggedAndNonPegged(t *testing.T) {
	conn := setup(t)
	for _, code := range []string{"BMD", "USD"} {
		providers, err := currency.Providers(ctx, conn, code)
		if err != nil || providers == nil {
			t.Errorf("%s providers = %v, %v", code, providers, err)
		}
	}
}

func TestNoPegMetadataForNonPegged(t *testing.T) {
	if usd := mustFind(t, setup(t), "USD"); usd.Peg != nil {
		t.Errorf("USD peg = %+v", usd.Peg)
	}
}
