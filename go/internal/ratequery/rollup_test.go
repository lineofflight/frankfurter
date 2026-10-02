package ratequery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/blend"
	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// spec/versions/v2/blended_rollups_spec.rb ("materialized responses") and the
// query-level cases of spec/rollup_spec.rb.

// body is Oj.dump(RateQuery.new(params).to_a) with force_live set as given.
func body(t *testing.T, conn *sql.DB, live bool, kv ...string) string {
	t.Helper()
	q := newQuery(t, conn, kv...)
	q.ForceLive = live
	data, err := json.Marshal(all(t, q))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// shapeWith is the spec's shape with overrides; an empty value drops the key,
// as compact does with nil.
func shapeWith(group string, overrides ...string) []string {
	m := map[string]string{
		"base": "CHF", "quotes": "USD,EUR,GBP,JPY", "from": d(latest().AddDate(0, 0, -370)), "to": d(latest()), "group": group,
	}
	for i := 0; i+1 < len(overrides); i += 2 {
		m[overrides[i]] = overrides[i+1]
	}
	var kv []string
	for k, v := range m {
		if v != "" {
			kv = append(kv, k, v)
		}
	}
	return kv
}

type groupedCase struct {
	group string
	model blend.Rollup
}

var groupedCases = []groupedCase{{"week", blend.Weekly}, {"month", blend.Monthly}}

func rebuildRollup(t *testing.T, conn *sql.DB, model blend.Rollup) {
	t.Helper()
	if err := model.Rebuild(ctx, conn, today()); err != nil {
		t.Fatal(err)
	}
}

// forbidRead makes any materialized grouped read fail the test for the rest of
// it.
func forbidRead(t *testing.T) {
	orig := readRollup
	readRollup = func(context.Context, blend.Rollup, db.Querier, time.Time, time.Time, time.Time) ([]currency.Blended, bool, error) {
		t.Error("unexpected materialized read")
		return nil, false, errors.New("unexpected materialized read")
	}
	t.Cleanup(func() { readRollup = orig })
}

func TestGroupedServesExactLiveBytesWithoutBlending(t *testing.T) {
	for _, c := range groupedCases {
		t.Run(c.group, func(t *testing.T) {
			conn := fixtures.New(t)
			shape := shapeWith(c.group)
			expected := body(t, conn, true, shape...)
			rebuildRollup(t, conn, c.model)
			calls := 0
			orig := blendRows
			blendRows = func(rows []rates.Row, base string, today time.Time) []currency.Blended {
				calls++
				return orig(rows, base, today)
			}
			actual := body(t, conn, false, shape...)
			blendRows = orig
			if actual != expected {
				t.Fatalf("table %.300s\nlive  %.300s", actual, expected)
			}
			if calls != 0 {
				t.Fatalf("blended %d times", calls)
			}
		})
	}
}

func TestGroupedPreservesConversionFiltersIdentitiesAndGaps(t *testing.T) {
	for _, c := range groupedCases {
		t.Run(c.group, func(t *testing.T) {
			conn := fixtures.New(t)
			rebuildRollup(t, conn, c.model)
			for _, o := range [][]string{
				{}, {"base", "USD"}, {"base", "AED"}, {"base", "ZAR"},
				{"quotes", "CHF"}, {"quotes", "CHF,EUR"}, {"quotes", "ZAR"}, {"quotes", ""},
				{"from", d(latest().AddDate(0, 0, -730))},
				{"from", d(latest()), "to", d(latest())},
				{"from", d(fixtures.RecentSunday())}, {"to", ""},
				{"from", d(today().AddDate(0, 0, 10)), "to", d(today().AddDate(0, 0, 30))},
			} {
				shape := shapeWith(c.group, o...)
				if table, live := body(t, conn, false, shape...), body(t, conn, true, shape...); table != live {
					t.Errorf("%v:\ntable %.300s\nlive  %.300s", o, table, live)
				}
			}
		})
	}
}

func TestGroupedFallsBackForMissingBuckets(t *testing.T) {
	for _, c := range groupedCases {
		t.Run(c.group, func(t *testing.T) {
			conn := fixtures.New(t)
			rebuildRollup(t, conn, c.model)
			shape := shapeWith(c.group)
			from, to := latest().AddDate(0, 0, -370), latest()
			rows, err := conn.QueryContext(ctx, c.model.Source.Dataset().Between(from, to, today()).
				Columns("DISTINCT bucket_date").OrderBy("bucket_date").SQL())
			if err != nil {
				t.Fatal(err)
			}
			var buckets []string
			for rows.Next() {
				var b db.NullDate
				rows.Scan(&b)
				buckets = append(buckets, d(b.Time))
			}
			rows.Close()
			for _, bucket := range []string{buckets[0], buckets[len(buckets)/2], buckets[len(buckets)-1]} {
				exec(t, conn, "DELETE FROM "+c.model.Table+" WHERE bucket_date = ?", bucket)
				if table, live := body(t, conn, false, shape...), body(t, conn, true, shape...); table != live {
					t.Errorf("without %s: table and live differ", bucket)
				}
				if _, err := c.model.Refresh(ctx, conn, []string{bucket}, today()); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestGroupedKeepsEmptyBucketSnapBack(t *testing.T) {
	for _, c := range groupedCases {
		t.Run(c.group, func(t *testing.T) {
			conn := fixtures.New(t)
			exec(t, conn, "DELETE FROM "+c.model.Source.Name)
			d1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
			d2 := d1.AddDate(0, 0, 40)
			insertBucket(t, conn, c.model, d1, "ECB", "USD", "EUR", 0.8)
			insertBucket(t, conn, c.model, d2, "ECB", "EUR", "JPY", 160.0)
			rebuildRollup(t, conn, c.model)
			shape := []string{"from", d(d2), "to", d(d2.AddDate(0, 0, 1)), "group", c.group, "base", "USD"}
			if live := body(t, conn, true, shape...); live != "[]" {
				t.Fatalf("live = %s", live)
			}
			if table := body(t, conn, false, shape...); table != "[]" {
				t.Fatalf("table = %s", table)
			}
		})
	}
}

func insertBucket(t *testing.T, conn *sql.DB, model blend.Rollup, bucket time.Time, provider, base, quote string, rate float64) {
	t.Helper()
	exec(t, conn, "INSERT INTO "+model.Source.Name+" (bucket_date, provider, base, quote, rate) VALUES (?, ?, ?, ?, ?)",
		d(bucket), provider, base, quote, rate)
}

func TestGroupedPreservesTransitionBridgesMetalsAndConsensus(t *testing.T) {
	pairs := []struct {
		provider, base, quote string
		rate                  float64
	}{
		{"LB", "LTL", "USD", 0.4}, {"LB", "LTL", "EUR", 0.3},
		{"LB", "EUR", "USD", 1.2}, {"LB", "EUR", "GBP", 0.8},
		{"BCBO", "XAU", "USD", 1200.0}, {"BCBO", "USD", "BOB", 6.9},
		{"ECB", "USD", "EUR", 0.82}, {"BOC", "USD", "EUR", 0.83},
		{"BOJ", "USD", "EUR", 0.84}, {"TST", "USD", "EUR", 20.0},
		{"UST", "USD", "CHF", 999.0},
	}
	for _, c := range groupedCases {
		t.Run(c.group, func(t *testing.T) {
			conn := fixtures.New(t)
			exec(t, conn, "DELETE FROM "+c.model.Source.Name)
			date := time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC)
			for _, p := range pairs {
				insertBucket(t, conn, c.model, date, p.provider, p.base, p.quote, p.rate)
			}
			rebuildRollup(t, conn, c.model)
			for _, base := range []string{"USD", "EUR", "LTL", "AED"} {
				shape := []string{"from", d(date), "to", d(date), "base", base, "group", c.group}
				for _, extra := range [][]string{nil, {"quotes", "EUR,GBP,XAU,CHF"}} {
					kv := append(append([]string{}, shape...), extra...)
					if table, live := body(t, conn, false, kv...), body(t, conn, true, kv...); table != live {
						t.Errorf("%v:\ntable %s\nlive  %s", kv, table, live)
					}
				}
			}
			records := query(t, conn, "from", d(date), "to", d(date), "base", "USD", "group", c.group)
			if find(records, "XAU") == nil {
				t.Error("no XAU")
			}
			if find(records, "CHF") != nil {
				t.Error("UST's CHF blended")
			}
			if eur := find(records, "EUR"); eur == nil || eur.Rate.Value >= 1 {
				t.Errorf("EUR = %+v", eur)
			}
		})
	}
}

func TestGroupedKeepsFilteredExpandedAndLiveQueriesOnTheirPath(t *testing.T) {
	for _, c := range groupedCases {
		t.Run(c.group, func(t *testing.T) {
			conn := fixtures.New(t)
			rebuildRollup(t, conn, c.model)
			var expected []string
			shapes := [][]string{
				append(shapeWith(c.group), "providers", "ECB"),
				append(shapeWith(c.group), "providers", "ECB,BOC"),
				append(shapeWith(c.group), "expand", "providers"),
			}
			for _, shape := range shapes {
				expected = append(expected, body(t, conn, true, shape...))
			}
			forbidRead(t)
			for i, shape := range shapes {
				if got := body(t, conn, false, shape...); got != expected[i] {
					t.Errorf("%v differs", shape)
				}
			}
			body(t, conn, true, shapeWith(c.group)...)
		})
	}
}

func TestGroupedKeepsSnapshotsWithGroupOnDailyPath(t *testing.T) {
	for _, c := range groupedCases {
		t.Run(c.group, func(t *testing.T) {
			conn := fixtures.New(t)
			shape := []string{"date", d(latest()), "base", "CHF", "quotes", "EUR"}
			expected := body(t, conn, false, shape...)
			forbidRead(t)
			if got := body(t, conn, false, append(shape, "group", c.group)...); got != expected {
				t.Fatalf("got %s, want %s", got, expected)
			}
		})
	}
}

// Both spans cross their chunk sizes (21 months weekly, 84 monthly), with
// source dates on either side of the year boundary. A sparse source may snap
// back to the same bucket in several successive chunks.
func TestGroupedPreservesSourceBucketsAndDuplicatesAcrossChunks(t *testing.T) {
	for _, c := range groupedCases {
		t.Run(c.group, func(t *testing.T) {
			conn := fixtures.New(t)
			exec(t, conn, "DELETE FROM "+c.model.Source.Name)
			buckets := []time.Time{
				time.Date(2005, 12, 29, 0, 0, 0, 0, time.UTC),
				time.Date(2006, 1, 5, 0, 0, 0, 0, time.UTC),
				time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC),
			}
			for _, b := range buckets {
				insertBucket(t, conn, c.model, b, "ECB", "USD", "EUR", 0.8)
			}
			rebuildRollup(t, conn, c.model)
			shape := []string{"from", "2005-12-30", "to", "2020-01-03", "base", "USD", "quotes", "EUR", "group", c.group}
			expected := body(t, conn, true, shape...)
			if got := body(t, conn, false, shape...); got != expected {
				t.Fatalf("table %.300s\nlive  %.300s", got, expected)
			}
			var records []Record
			json.Unmarshal([]byte(expected), &records)
			if len(records) <= len(buckets) {
				t.Fatalf("%d records", len(records))
			}
		})
	}
}

// spec/rollup_spec.rb, query level.

func TestMonthlyGroupedQueryIncludesPartialFirstMonth(t *testing.T) {
	records := query(t, fixtures.New(t), "from", d(latest().AddDate(0, 0, -40)), "to", d(latest()), "group", "month")
	if n := len(uniq(dates(records))); n < 2 {
		t.Fatalf("%d monthly buckets for a cross-month range", n)
	}
}

func TestWeeklyGroupedQueryIncludesPartialFirstWeek(t *testing.T) {
	records := query(t, fixtures.New(t), "from", d(latest().AddDate(0, 0, -20)), "to", d(latest()), "group", "week")
	if n := len(uniq(dates(records))); n < 3 {
		t.Fatalf("%d weekly buckets for a 20-day range", n)
	}
}

func TestSingleDateGroupedQuery(t *testing.T) {
	conn := fixtures.New(t)
	for _, group := range []string{"month", "week"} {
		if len(query(t, conn, "date", d(latest()), "group", group)) == 0 {
			t.Errorf("group=%s: no records", group)
		}
	}
}

// The cache key derives from the raw max date, so it stays stable when rollup
// content changes within a day.
func TestCacheKeyDerivesFromRawRates(t *testing.T) {
	conn := fixtures.New(t)
	shape := []string{"from", d(latest().AddDate(0, 0, -30)), "to", d(latest()), "group", "month"}
	before, err := newQuery(t, conn, shape...).CacheKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	insert(t, conn, "ECB", latest(), "EUR", "XTS", 42.0)
	if err := blend.RefreshProviderRollups(ctx, conn, "ECB", []time.Time{latest()}, today()); err != nil {
		t.Fatal(err)
	}
	after, err := newQuery(t, conn, shape...).CacheKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("key changed: %s -> %s", before, after)
	}
}

// spec/versions/v2_spec.rb "iterates over range results".
func TestIteratesOverRangeResults(t *testing.T) {
	conn := fixtures.New(t)
	var records []Record
	q := newQuery(t, conn, "from", d(fixtures.BusinessDay(60)), "to", d(fixtures.BusinessDay(30)))
	if err := q.Each(ctx, func(r Record) error { records = append(records, r); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 || records[0].Date == "" || records[0].Rate.Value == 0 {
		t.Fatalf("records = %+v", records)
	}
}
