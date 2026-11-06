package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/blend"
	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
	"github.com/lineofflight/frankfurter/go/internal/provider"
	"github.com/lineofflight/frankfurter/go/internal/ratequery"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// refreshRollups is Provider#refresh_rollups: the provider rollups and, for
// blending providers, the grouped blends.
func (a *v2App) refreshRollups(key string, dates ...time.Time) {
	a.t.Helper()
	if err := blend.RefreshProviderRollups(context.Background(), a.db, key, dates, fixtures.Today()); err != nil {
		a.t.Fatal(err)
	}
}

func (a *v2App) rate() float64 {
	a.t.Helper()
	a.ok()
	return a.object()["rate"].(float64)
}

func (a *v2App) count(query string, args ...any) int {
	a.t.Helper()
	var n int
	if err := a.db.QueryRow(query, args...).Scan(&n); err != nil {
		a.t.Fatal(err)
	}
	return n
}

// blendableCount counts table t's blendable rows matching cond.
func (a *v2App) blendableCount(t rates.Table, cond string) int {
	a.t.Helper()
	filter, err := rates.LoadBlendFilter(context.Background(), a.db)
	if err != nil {
		a.t.Fatal(err)
	}
	return a.count(t.Blendable(filter).Filter(cond).Columns("count(*)").SQL())
}

// spec/versions/v2/provider_currencies_spec.rb

func newZZZApp(t *testing.T) *v2App {
	a := newV2App(t)
	a.insert("ECB", latest(), "EUR", "ZZZ", 2.345678)
	a.refreshRollups("ECB", fixtures.LatestDate())
	return a
}

func TestServesUnknownStoredQuoteThroughEitherProviderRoute(t *testing.T) {
	a := newZZZApp(t)
	for _, path := range []string{"/providers/ECB/rate/EUR/ZZZ", "/rate/EUR/ZZZ?providers=ECB"} {
		a.get(path)
		if r := a.rate(); r != 2.345678 {
			t.Errorf("%s: %v", path, r)
		}
	}
}

func TestDerivesRateUsingUnknownStoredBase(t *testing.T) {
	a := newZZZApp(t)
	a.get("/providers/ECB/rate/ZZZ/EUR")
	if r := a.rate(); r != 0.42632 {
		t.Fatalf("rate = %v", r)
	}
}

func TestServesRoundedUnknownQuotesFromProviderRollups(t *testing.T) {
	a := newZZZApp(t)
	from := db.FormatDate(fixtures.LatestDate().AddDate(0, 0, -7))
	a.get("/providers/ECB/rates?base=EUR&quotes=ZZZ&from=" + from + "&to=" + latest() + "&group=month")
	a.ok()
	if r := a.array()[0]["rate"]; r != 2.3457 {
		t.Fatalf("rate = %v", r)
	}
}

func TestServesModernSucreOnlyThroughProviderRoutes(t *testing.T) {
	a := newZZZApp(t)
	a.insert("CBKKW", latest(), "ECS", "KWD", 0.000012)
	a.refreshRollups("CBKKW", fixtures.LatestDate())
	a.get("/providers/CBKKW/rate/ECS/KWD")
	if r := a.rate(); r != 0.000012 {
		t.Fatalf("rate = %v", r)
	}
	for _, table := range rates.Tables {
		if n := a.blendableCount(table, "base = 'ECS'"); n != 0 {
			t.Errorf("%s: %d blendable ECS rows", table.Name, n)
		}
	}
	a.get("/rate/ECS/KWD")
	a.status(404)
}

func TestRejectsUnknownCodesOutsideSelectedProvidersRows(t *testing.T) {
	a := newZZZApp(t)
	for _, path := range []string{"/rate/EUR/ZZZ", "/providers/BOC/rate/EUR/ZZZ", "/providers/ECB/rate/EUR/QQQ"} {
		a.get(path)
		a.status(422)
	}
}

func TestReportsUnknownCodesOnProviderMetadataOnly(t *testing.T) {
	a := newZZZApp(t)
	a.refreshSummaries("ECB", "EUR", "ZZZ")
	a.get("/providers/ECB")
	a.ok()
	if u := anyStrings(a.object()["unknown_currencies"]); !slices.Equal(u, []string{"ZZZ"}) {
		t.Fatalf("unknown = %v", u)
	}
	a.get("/currency/ZZZ")
	a.status(404)
}

func TestKeepsProviderVisibleWhenAllItsCodesAreUnknown(t *testing.T) {
	a := newZZZApp(t)
	a.exec("DELETE FROM rates WHERE provider = 'ECB'")
	a.exec("DELETE FROM currency_coverages WHERE provider_key = 'ECB'")
	a.insert("ECB", latest(), "ZZZ", "QQQ", 2.0)
	a.refreshSummaries("ECB", "ZZZ", "QQQ")
	a.get("/providers/ECB")
	a.ok()
	entry := a.object()
	if u := anyStrings(entry["unknown_currencies"]); !slices.Equal(u, []string{"QQQ", "ZZZ"}) {
		t.Fatalf("unknown = %v", u)
	}
	if c := anyStrings(entry["currencies"]); len(c) != 0 {
		t.Fatalf("currencies = %v", c)
	}
	if entry["start_date"] != latest() || entry["end_date"] != latest() || entry["publishes_missed"] != 0.0 {
		t.Fatalf("entry = %v", entry)
	}
	a.get("/providers")
	if listed := findRow(a.array(), "key", "ECB"); !reflect.DeepEqual(listed, entry) {
		t.Fatalf("listed %v, entry %v", listed, entry)
	}
}

// spec/versions/v2/non_currency_rates_spec.rb

func newIndexApp(t *testing.T) *v2App {
	a := newV2App(t)
	date := fixtures.LatestDate()
	for _, table := range rates.Tables {
		a.exec("DELETE FROM " + table.Name)
	}
	a.insert("NB", db.FormatDate(date.AddDate(0, 0, -3)), "USD", "NOK", 10.0)
	a.insert("NB", db.FormatDate(date.AddDate(0, 0, -3)), "EUR", "NOK", 11.0)
	a.insert("ECB", db.FormatDate(date.AddDate(0, 0, -7)), "EUR", "USD", 1.2)
	a.refreshRollups("NB", date.AddDate(0, 0, -3))
	a.refreshRollups("ECB", date.AddDate(0, 0, -7))
	return a
}

func (a *v2App) addIndices() {
	a.insert("NB", latest(), "I44", "NOK", 110.6)
	a.insert("NB", latest(), "TWI", "NOK", 115.432)
	a.refreshRollups("NB", fixtures.LatestDate())
	a.refreshSummaries("NB", "I44", "TWI", "NOK")
}

// records is RateQuery.new(params).to_a.
func (a *v2App) records(kv ...string) []ratequery.Record {
	a.t.Helper()
	params := ratequery.Params{}
	for i := 0; i+1 < len(kv); i += 2 {
		params[kv[i]] = kv[i+1]
	}
	q, err := ratequery.New(context.Background(), a.db, params, ratequery.Options{Today: fixtures.Today()})
	if err != nil {
		a.t.Fatal(err)
	}
	records, err := q.All(context.Background())
	if err != nil {
		a.t.Fatal(err)
	}
	return records
}

func TestKeepsCurrencySnapshotsUnchangedWhenIndexArrives(t *testing.T) {
	a := newIndexApp(t)
	queries := [][]string{
		{"providers", "NB,ECB", "base", "USD", "quotes", "EUR", "date", latest(), "expand", "providers"},
		{"providers", "NB,ECB", "base", "USD", "quotes", "EUR"},
	}
	var before [][]ratequery.Record
	for _, q := range queries {
		before = append(before, a.records(q...))
	}
	if r := before[0][0].Rate.Value; r != 0.88049 {
		t.Fatalf("rate = %v", r)
	}
	a.addIndices()
	for i, q := range queries {
		if after := a.records(q...); !reflect.DeepEqual(after, before[i]) {
			t.Errorf("%v changed: %+v -> %+v", q, before[i], after)
		}
	}
}

func TestKeepsIndicesOutOfMultiProviderRanges(t *testing.T) {
	for _, group := range []string{"", "week", "month"} {
		t.Run("group="+group, func(t *testing.T) {
			a := newIndexApp(t)
			q := []string{"providers", "NB,ECB", "base", "USD", "from",
				db.FormatDate(fixtures.LatestDate().AddDate(0, 0, -7)), "to", latest()}
			if group != "" {
				q = append(q, "group", group)
			}
			before := a.records(q...)
			if len(before) == 0 {
				t.Fatal("empty")
			}
			a.addIndices()
			if after := a.records(q...); !reflect.DeepEqual(after, before) {
				t.Fatalf("changed: %+v -> %+v", before, after)
			}
		})
	}
}

func TestKeepsRangeStartSnapshotAfterIndexOnlyPublication(t *testing.T) {
	a := newIndexApp(t)
	q := []string{"providers", "NB,ECB", "base", "USD", "quotes", "EUR", "from", latest(), "to", latest()}
	before := a.records(q...)
	a.addIndices()
	if after := a.records(q...); !reflect.DeepEqual(after, before) {
		t.Fatalf("changed: %+v -> %+v", before, after)
	}
}

func TestRetainsNativeIndexObservationsAndSingleProviderConversions(t *testing.T) {
	a := newIndexApp(t)
	a.addIndices()
	a.get("/providers/NB/rate/I44/NOK?date=" + latest())
	if r := a.rate(); r != 110.6 {
		t.Fatalf("I44/NOK = %v", r)
	}
	a.get("/providers/NB/rate/NOK/I44?date=" + latest())
	if r := a.rate(); r != 0.00904 {
		t.Fatalf("NOK/I44 = %v", r)
	}
	for _, table := range rates.Tables {
		if n := a.count("SELECT count(*) FROM " + table.Name + " WHERE provider = 'NB' AND base IN ('I44', 'TWI')"); n != 2 {
			t.Errorf("%s: %d index rows", table.Name, n)
		}
		if n := a.blendableCount(table, "base IN ('I44', 'TWI')"); n != 0 {
			t.Errorf("%s: %d blendable index rows", table.Name, n)
		}
	}
	a.get("/currencies")
	codes := field(a.array(), "iso_code")
	if has(codes, "I44") || has(codes, "TWI") {
		t.Fatalf("codes = %v", codes)
	}
}

func TestRejectsIndexCodesWhenSelectingSeveralProviders(t *testing.T) {
	a := newIndexApp(t)
	a.addIndices()
	for _, path := range []string{"/rate/I44/NOK", "/rate/NOK/I44"} {
		a.get(path + "?providers=NB,ECB&date=" + latest())
		a.status(422)
	}
	a.get("/rate/I44/NOK?providers=NB,NB&date=" + latest())
	a.ok()
}

func TestAcknowledgesReviewedIndicesButReportsNewLabels(t *testing.T) {
	a := newIndexApp(t)
	a.addIndices()
	a.insert("NB", latest(), "ZZZ", "NOK", 100)
	a.insert("ECB", latest(), "EUR", "I44", 100)
	a.refreshSummaries("NB", "ZZZ")
	a.refreshSummaries("ECB", "I44")
	a.get("/providers/NB")
	a.ok()
	entry := a.object()
	if u := anyStrings(entry["unknown_currencies"]); !slices.Equal(u, []string{"ZZZ"}) {
		t.Fatalf("unknown = %v", u)
	}
	if c := anyStrings(entry["currencies"]); has(c, "I44") || has(c, "TWI") {
		t.Fatalf("currencies = %v", c)
	}
	ecb, err := provider.Find(context.Background(), a.db, "ECB")
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := ecb.UnknownCurrencies(context.Background(), a.db)
	if err != nil || !has(unknown, "I44") {
		t.Fatalf("ECB unknown = %v (%v)", unknown, err)
	}
}

// indexAdapter publishes one RBA trade-weighted index observation.
type indexAdapter struct{ date time.Time }

func (f indexAdapter) Fetch(context.Context, time.Time, time.Time) ([]adapter.Rate, error) {
	return []adapter.Rate{{Date: f.date, Base: "AUD", Quote: "FXRTWI", Rate: 61.4}}, nil
}
func (indexAdapter) BackfillRange() int { return 0 }
func (indexAdapter) LeadDays() int      { return 0 }
func (indexAdapter) Revises() bool      { return false }

func dumpTable(t *testing.T, a *v2App, table, order string) [][3]any {
	t.Helper()
	rows, err := a.db.Query("SELECT * FROM " + table + " ORDER BY " + order)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [][3]any
	for rows.Next() {
		var r [3]any
		if err := rows.Scan(&r[0], &r[1], &r[2]); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func TestIngestsRBAIndexWithoutChangingConversionsOrBlends(t *testing.T) {
	a := newIndexApp(t)
	ctx := context.Background()
	date := fixtures.LatestDate()
	a.insert("RBA", db.FormatDate(date.AddDate(0, 0, -3)), "AUD", "USD", 0.7)
	a.refreshRollups("RBA", date.AddDate(0, 0, -3))
	if err := blend.RebuildDaily(ctx, a.db, fixtures.Today()); err != nil {
		t.Fatal(err)
	}
	for _, m := range blend.Rollups {
		if err := m.Rebuild(ctx, a.db, fixtures.Today()); err != nil {
			t.Fatal(err)
		}
	}
	tables := []struct{ name, order string }{
		{"blended_rates", "quote, date"}, {"blended_weekly_rates", "quote, bucket_date"},
		{"blended_monthly_rates", "quote, bucket_date"},
	}
	var before [][][3]any
	for _, tb := range tables {
		before = append(before, dumpTable(t, a, tb.name, tb.order))
	}
	queries := [][]string{
		{"base", "USD", "quotes", "AUD", "date", latest()},
		{"providers", "RBA", "base", "USD", "quotes", "AUD", "date", latest()},
		{"providers", "RBA,NB,ECB", "base", "USD", "quotes", "AUD", "date", latest()},
	}
	var ratesBefore [][]ratequery.Record
	for _, q := range queries {
		ratesBefore = append(ratesBefore, a.records(q...))
	}

	rba, err := provider.Find(ctx, a.db, "RBA")
	if err != nil {
		t.Fatal(err)
	}
	in := &provider.Ingester{
		DB: a.db, Today: fixtures.Today, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Adapter: func(string) (adapter.Adapter, error) { return indexAdapter{date}, nil },
	}
	in.BackfillAfter(ctx, *rba, date.AddDate(0, 0, -1))

	for i, tb := range tables {
		if after := dumpTable(t, a, tb.name, tb.order); !reflect.DeepEqual(after, before[i]) {
			t.Errorf("%s changed", tb.name)
		}
	}
	for i, q := range queries {
		if after := a.records(q...); !reflect.DeepEqual(after, ratesBefore[i]) {
			t.Errorf("%v changed: %+v -> %+v", q, ratesBefore[i], after)
		}
	}
	a.get("/providers/RBA/rate/AUD/FXRTWI?date=" + latest())
	if r := a.rate(); r != 61.4 {
		t.Fatalf("rate = %v", r)
	}
	a.get("/rate/AUD/FXRTWI?providers=NB,RBA&date=" + latest())
	a.status(http.StatusUnprocessableEntity)
	if rba, _ = provider.Find(ctx, a.db, "RBA"); rba == nil {
		t.Fatal("no RBA")
	}
	if unknown, err := rba.UnknownCurrencies(ctx, a.db); err != nil || len(unknown) != 0 {
		t.Fatalf("unknown = %v (%v)", unknown, err)
	}
	if c, err := currency.FindCurrency(ctx, a.db, "FXRTWI"); err != nil || c != nil {
		t.Fatalf("FXRTWI = %+v (%v)", c, err)
	}
	for _, table := range rates.Tables {
		if n := a.count("SELECT count(*) FROM " + table.Name + " WHERE provider = 'RBA' AND quote = 'FXRTWI'"); n != 1 {
			t.Errorf("%s: %d FXRTWI rows", table.Name, n)
		}
	}
}
