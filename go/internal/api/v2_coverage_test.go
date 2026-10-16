package api

import (
	"context"
	"errors"
	"math"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/blend"
	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
	"github.com/lineofflight/frankfurter/go/internal/heavyslots"
	"github.com/lineofflight/frankfurter/go/internal/ratequery"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// spec/versions/v2/coverage_spec.rb

func newCoverageApp(t *testing.T) *v2App {
	a := newV2App(t)
	for _, table := range []string{"rates", "blended_rates", "currencies", "currency_coverages", "currency_exclusions"} {
		a.exec("DELETE FROM " + table)
	}
	return a
}

func (a *v2App) observation(provider, date, base, quote string) {
	a.insert(provider, date, base, quote, 1.2)
}

func (a *v2App) coverage(kv ...string) map[string]any {
	a.t.Helper()
	a.getParams("/coverage", kv...)
	a.ok()
	return a.object()
}

// assertBounds checks the coverage of params and that a snapshot at each bound
// returns records. An empty last means the same as first; "-" means null.
func (a *v2App) assertBounds(params []string, first string, last ...string) {
	a.t.Helper()
	end := first
	if len(last) > 0 {
		end = last[0]
	}
	result := a.coverage(params...)
	got := [2]any{result["start_date"], result["end_date"]}
	want := [2]any{nullableBound(first), nullableBound(end)}
	if got != want {
		a.t.Fatalf("coverage %v = %v, want %v", params, got, want)
	}
	for _, d := range uniqStrings([]string{first, end}) {
		if d == "-" {
			continue
		}
		a.getParams("/rates", append(slices.Clone(params), "date", d)...)
		a.ok()
		if len(a.array()) == 0 {
			a.t.Fatalf("rates %v on %s: empty", params, d)
		}
	}
}

func nullableBound(s string) any {
	if s == "-" {
		return nil
	}
	return s
}

func (a *v2App) rebuild(stored bool) {
	if stored {
		a.rebuildDaily()
	}
}

func TestCoverageSeparatesDefaultBlendFromBISMonthlyHistory(t *testing.T) {
	a := newCoverageApp(t)
	ctx := context.Background()
	a.insert("BIS", "1900-01-31", "USD", "ZAR", 0.4107)
	a.observation("JPC", "1901-01-06", "USD", "EUR")
	a.observation("BOC", "1950-01-03", "USD", "CAD")
	a.observation("ECB", "1999-01-04", "EUR", "USD")
	if err := rates.RefreshSummaries(ctx, a.db, []string{"USD", "ZAR", "EUR", "CAD"}, ""); err != nil {
		t.Fatal(err)
	}

	// Existing catalogues already distinguish publication sources, but not a
	// query's base.
	bis, err := currency.WithProviders(ctx, a.db, []string{"BIS"})
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.IndexFunc(bis, func(c currency.Currency) bool { return c.ISOCode == "ZAR" }); i < 0 ||
		db.FormatDate(bis[i].StartDate) != "1900-01-31" {
		t.Fatalf("BIS catalogue = %+v", bis)
	}
	if usd, err := currency.FindCurrency(ctx, a.db, "USD"); err != nil || db.FormatDate(usd.StartDate) != "1950-01-03" {
		t.Fatalf("USD = %+v (%v)", usd, err)
	}
	a.assertBounds(nil, "1999-01-04")
	a.assertBounds([]string{"base", "usd"}, "1950-01-03", "1999-01-04")
	a.assertBounds([]string{"providers", "bis", "base", "usd", "quotes", "zar"}, "1900-01-31")
	a.assertBounds([]string{"providers", "bis"}, "-")

	a.get("/rates?date=1900-01-31")
	if a.body() != "[]" {
		t.Fatalf("body = %s", a.body())
	}
	a.rebuildDaily()
	if n := a.count("SELECT count(*) FROM blended_rates WHERE date < '1950-01-03'"); n != 0 {
		t.Fatalf("%d stored before 1950", n)
	}
	a.assertBounds(nil, "1999-01-04")
	a.assertBounds([]string{"providers", "BIS", "base", "USD", "quotes", "ZAR"}, "1900-01-31")
}

func eachStore(t *testing.T, fn func(t *testing.T, stored bool)) {
	for _, stored := range []bool{false, true} {
		name := "live"
		if stored {
			name = "materialized"
		}
		t.Run(name, func(t *testing.T) { fn(t, stored) })
	}
}

func TestCoverageStartsWhenBothLegsExist(t *testing.T) {
	eachStore(t, func(t *testing.T, stored bool) {
		a := newCoverageApp(t)
		a.observation("BOC", "2000-01-01", "USD", "JPY")
		a.observation("ECB", "2000-01-10", "USD", "EUR")
		a.rebuild(stored)
		a.assertBounds([]string{"base", "EUR", "quotes", "JPY"}, "2000-01-10")
		a.assertBounds([]string{"base", "EUR", "quotes", "USD"}, "2000-01-10")
		a.assertBounds([]string{"base", "EUR", "quotes", "EUR"}, "2000-01-10")
		a.assertBounds([]string{"base", "USD", "quotes", "USD"}, "2000-01-01", "2000-01-10")
		a.get("/rates?base=EUR&quotes=JPY&date=2000-01-01")
		if a.body() != "[]" {
			t.Fatalf("body = %s", a.body())
		}
	})
}

func TestCoverageDoesNotInferPairsFromOverlappingBounds(t *testing.T) {
	eachStore(t, func(t *testing.T, stored bool) {
		a := newCoverageApp(t)
		a.observation("ECB", "2000-01-01", "USD", "EUR")
		a.observation("BOC", "2000-02-01", "USD", "JPY")
		a.observation("ECB", "2000-03-01", "USD", "EUR")
		a.rebuild(stored)
		a.assertBounds([]string{"base", "EUR", "quotes", "JPY"}, "-")
		a.assertBounds([]string{"base", "EUR", "quotes", "JPY,USD"}, "2000-01-01", "2000-03-01")
	})
}

func TestCoverageIncludesCarryForwardBoundaryWithoutFutureDays(t *testing.T) {
	eachStore(t, func(t *testing.T, stored bool) {
		a := newCoverageApp(t)
		a.observation("ECB", "2000-01-01", "USD", "EUR")
		a.observation("BOC", "2000-01-15", "USD", "JPY")
		a.observation("BOC", "2000-01-16", "USD", "JPY")
		a.rebuild(stored)
		a.assertBounds([]string{"base", "EUR", "quotes", "JPY"}, "2000-01-15")
	})
}

func TestCoverageDoesNotAdvertiseDisconnectedUnknownOrExpiredRows(t *testing.T) {
	a := newCoverageApp(t)
	a.observation("ECB", "1980-01-01", "GBP", "JPY")
	a.observation("ECB", "1981-01-01", "USD", "ZZZ")
	a.observation("ECB", "2017-01-01", "USD", "BYR")
	a.assertBounds([]string{"base", "GBP"}, "-")
	a.assertBounds([]string{"base", "USD"}, "-")
	a.assertBounds([]string{"providers", "ECB", "base", "GBP", "quotes", "JPY"}, "1980-01-01")
	a.assertBounds([]string{"providers", "ECB", "base", "USD", "quotes", "ZZZ"}, "1981-01-01")
	a.assertBounds([]string{"providers", "ECB", "base", "USD", "quotes", "BYR"}, "2017-01-01")
}

func TestCoverageStableWhenMaterializationOmitsDisconnectedDate(t *testing.T) {
	a := newCoverageApp(t)
	a.observation("ECB", "2000-01-01", "USD", "EUR")
	a.observation("BOC", "2000-01-10", "GBP", "JPY")
	a.assertBounds(nil, "2000-01-01", "2000-01-10")
	a.rebuildDaily()
	if ok, err := blend.DailyReady(context.Background(), a.db); err != nil || !ok {
		t.Fatalf("ready = %v (%v)", ok, err)
	}
	a.assertBounds(nil, "2000-01-01", "2000-01-10")
}

func TestCoverageUsesSelectedProvidersWiderWindow(t *testing.T) {
	a := newCoverageApp(t)
	a.observation("BIS", "2000-01-01", "USD", "EUR")
	a.observation("BIS", "2000-02-10", "USD", "JPY")
	a.observation("BIS", "2000-03-01", "USD", "JPY")
	a.assertBounds([]string{"providers", "BIS", "base", "EUR", "quotes", "JPY"}, "2000-02-10")
}

func TestCoverageUsesMultiProviderUSDBridge(t *testing.T) {
	a := newCoverageApp(t)
	a.observation("ECB", "2000-01-01", "EUR", "JPY")
	a.observation("BOC", "2000-01-01", "CAD", "GBP")
	a.assertBounds([]string{"providers", "ECB,BOC", "base", "EUR", "quotes", "JPY"}, "-")
	a.observation("ECB", "2000-01-03", "EUR", "USD")
	a.assertBounds([]string{"providers", "ECB,BOC", "base", "EUR", "quotes", "JPY"}, "2000-01-03")
}

func TestCoverageHonorsPegInceptionAndProviderBypass(t *testing.T) {
	eachStore(t, func(t *testing.T, stored bool) {
		a := newCoverageApp(t)
		a.observation("BOC", "1997-11-01", "USD", "CAD")
		a.observation("BOC", "1997-11-03", "USD", "CAD")
		a.rebuild(stored)
		a.assertBounds([]string{"base", "AED", "quotes", "CAD"}, "1997-11-03")
		a.assertBounds([]string{"base", "USD", "quotes", "AED"}, "1997-11-03")
		a.assertBounds([]string{"base", "USD", "quotes", "AED", "providers", "BOC"}, "-")
		a.assertBounds([]string{"base", "AED", "providers", "BOC,ECB"}, "-")
	})
}

func TestCoverageReleasesSlotWhenDeadlineExpires(t *testing.T) {
	a := newCoverageApp(t)
	a.observation("ECB", "2000-01-01", "EUR", "USD")
	slots := heavyslots.New(1)
	q, err := ratequery.New(context.Background(), a.db, ratequery.Params{},
		ratequery.Options{Today: fixtures.Today(), Deadline: time.Now().Add(-time.Second), Slots: slots})
	if err != nil {
		t.Fatal(err)
	}
	var deadline *ratequery.DeadlineError
	if _, err := q.History(context.Background()); !errors.As(err, &deadline) {
		t.Fatalf("err = %v", err)
	}
	a.s.HeavySlots = slots
	a.assertBounds(nil, "2000-01-01")
}

func TestCoverageReturnsNullBoundsForEmptyFeed(t *testing.T) {
	a := newCoverageApp(t)
	a.assertBounds(nil, "-")
	a.assertBounds([]string{"base", "USD", "quotes", "USD"}, "-")
	a.assertBounds([]string{"providers", "MISSING"}, "-")
}

func TestCoverageIdentifiesFeedAndNormalizesFilters(t *testing.T) {
	a := newCoverageApp(t)
	c := a.coverage()
	if c["base"] != "EUR" || c["quotes"] != nil || c["providers"] != nil {
		t.Fatalf("coverage = %v", c)
	}
	c = a.coverage("base", "usd", "providers", "bis", "quotes", "zar")
	if c["base"] != "USD" || !slices.Equal(anyStrings(c["quotes"]), []string{"ZAR"}) ||
		!slices.Equal(anyStrings(c["providers"]), []string{"BIS"}) {
		t.Fatalf("coverage = %v", c)
	}
}

func TestCoverageRejectsUnsupportedFiltersAndInvalidCurrencies(t *testing.T) {
	a := newCoverageApp(t)
	for _, kv := range [][]string{{"date", "1900-01-31"}, {"group", "month"}, {"scope", "all"}, {"base", "NOTREAL"}} {
		a.getParams("/coverage", kv...)
		a.status(422)
	}
}

// spec/reciprocal_consistency_spec.rb: contract tests for reciprocal
// consistency, cross-rate transitivity and pegged-base anchoring.

func (a *v2App) rateFor(base, quote string) float64 {
	a.t.Helper()
	a.get("/rates?base=" + base + "&quotes=" + quote)
	a.ok()
	row := findRow(a.array(), "quote", quote)
	if row == nil {
		a.t.Fatalf("no rate for %s->%s", base, quote)
	}
	return row["rate"].(float64)
}

func round4(v float64) float64 { return math.Round(v*1e4) / 1e4 }

func TestReciprocalRatesRoundToOne(t *testing.T) {
	a := newV2App(t)
	for _, p := range [][2]string{{"USD", "GBP"}, {"USD", "CAD"}} {
		if product := a.rateFor(p[0], p[1]) * a.rateFor(p[1], p[0]); round4(product) != 1.0 {
			t.Errorf("%v: product %v", p, product)
		}
	}
}

func TestTransitivityHoldsAcrossUSDEURGBP(t *testing.T) {
	a := newV2App(t)
	product := a.rateFor("USD", "EUR") * a.rateFor("EUR", "GBP") * a.rateFor("GBP", "USD")
	if round4(product) != 1.0 {
		t.Fatalf("product %v", product)
	}
}

func TestPeggedBaseResolvesToPeg(t *testing.T) {
	a := newV2App(t)
	if rate := a.rateFor("AED", "USD"); rate != math.Round(1.0/3.6725*1e5)/1e5 {
		t.Fatalf("rate %v", rate)
	}
}

// spec/versions/v2/blended_rollups_spec.rb, "Grouped HTTP responses": grouped
// JSON, NDJSON and CSV stream the same bytes (and ETag) from the stored blends
// as from the live blend. That the table served them is shown by tampering with
// it afterwards, which the Ruby spec shows by stubbing the blender.
func TestStreamsUnchangedGroupedResponsesFromStoredBlends(t *testing.T) {
	for _, c := range []struct {
		group string
		model blend.Rollup
	}{{"week", blend.Weekly}, {"month", blend.Monthly}} {
		t.Run(c.group, func(t *testing.T) {
			a := newV2App(t)
			query := "?base=CHF&quotes=USD,EUR,GBP,JPY&from=" + db.FormatDate(fixtures.LatestDate().AddDate(0, 0, -370)) +
				"&to=" + latest() + "&group=" + c.group
			for _, f := range [][2]string{{"/rates", "application/json"}, {"/rates", "application/x-ndjson"},
				{"/rates.csv", "text/csv"}} {
				a.exec("DELETE FROM " + c.model.Table)
				a.get(f[0]+query, "Accept", f[1])
				a.ok()
				expected, etag := a.body(), a.header("ETag")
				if err := c.model.Rebuild(context.Background(), a.db, fixtures.Today()); err != nil {
					t.Fatal(err)
				}
				a.get(f[0]+query, "Accept", f[1])
				a.ok()
				if a.body() != expected || a.header("ETag") != etag {
					t.Fatalf("%s %s: stored blends changed the response", f[0], f[1])
				}
				a.exec("UPDATE " + c.model.Table + " SET rate = rate * 2")
				a.get(f[0]+query, "Accept", f[1])
				if a.body() == expected {
					t.Fatalf("%s %s: not served from the stored blends", f[0], f[1])
				}
			}
		})
	}
}

// spec/app_spec.rb, the v2 cases.

func TestServesV2Root(t *testing.T) {
	a := newTestApp(t)
	a.get("/v2")
	a.status(http.StatusOK)
	j := a.json()
	if j["version"] != "v2" || j["status"] != "current" || j["openapi"] != "/v2/openapi.json" {
		t.Fatalf("root = %v", j)
	}
	if a.header("X-Robots-Tag") != "" {
		t.Fatal("/v2 is not indexable")
	}
}

// Through the full middleware stack: the timeout middleware must not swallow a
// 503 the query generated after its own (later-starting) deadline expired.
func TestDeliversV2Deadline503ThroughMiddleware(t *testing.T) {
	a := newTestApp(t)
	stubRateQuery(t, fakeQuery{each: func(func(ratequery.Record) error) error {
		return &ratequery.DeadlineError{Timeout: 90 * time.Second}
	}})
	a.get("/v2/rates?from=2024-01-01&to=2024-02-01")
	a.status(http.StatusServiceUnavailable)
	if a.header("Cache-Control") != "no-store" || !strings.Contains(a.json()["message"].(string), "timeout") {
		t.Fatalf("headers %v, body %s", a.res.Header(), a.res.Body.String())
	}
}

func TestDoesNotCache503FromHeavyComputeCap(t *testing.T) {
	a := newTestApp(t)
	slots := heavyslots.New(1)
	slots.TryAcquire()
	a.h = (&Server{DB: a.db, Today: fixtures.Today, HeavySlots: slots}).Handler()
	a.get("/v2/rates?providers=ecb&from=" + bday(60) + "&to=" + latest())
	a.status(http.StatusServiceUnavailable)
	if a.header("Cache-Control") != "no-store" || a.header("Retry-After") != "30" {
		t.Fatalf("headers %v", a.res.Header())
	}
}

func TestV2ErrorResponsesAreNotCached(t *testing.T) {
	a := newTestApp(t)
	for _, c := range []struct {
		path   string
		status int
	}{{"/v2/rates?date=not-a-date", 422}, {"/v2/currency/xyz", 404}, {"/v2/currencies.csv", 406}} {
		a.get(c.path)
		if a.res.Code != c.status || a.header("Cache-Control") != "no-store" {
			t.Errorf("%s: %d, Cache-Control %q", c.path, a.res.Code, a.header("Cache-Control"))
		}
	}
}
