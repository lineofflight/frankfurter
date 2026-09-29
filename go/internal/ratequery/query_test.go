package ratequery

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/blend"
	"github.com/lineofflight/frankfurter/go/internal/currency"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
	"github.com/lineofflight/frankfurter/go/internal/heavyslots"
)

// spec/versions/v2/rate_query_spec.rb

// withMonthlyProvider adds TST, a monthly provider, with rows on rowsOn and a monthly rollup.
func withMonthlyProvider(t *testing.T, conn *sql.DB, rowsOn time.Time) {
	t.Helper()
	exec(t, conn, "INSERT INTO providers (key, name, frequency) VALUES ('TST', 'Test', 'monthly')")
	insert(t, conn, "TST", rowsOn, "EUR", "USD", 9.0)
	insert(t, conn, "TST", rowsOn, "EUR", "GBP", 8.0)
	exec(t, conn, "INSERT INTO monthly_rates (bucket_date, provider, base, quote, rate) VALUES (?, 'TST', 'EUR', 'USD', 9.0)",
		d(time.Date(rowsOn.Year(), rowsOn.Month(), 1, 0, 0, 0, 0, time.UTC)))
}

func TestKeepsMonthlyProviderOutOfUnfilteredBlend(t *testing.T) {
	conn := fixtures.New(t)
	withMonthlyProvider(t, conn, latest())
	record := query(t, conn, "base", "EUR", "quotes", "USD", "date", d(latest()), "expand", "providers")[0]
	for _, p := range record.Providers {
		if p.Key == "TST" {
			t.Fatal("TST contributes to the blend")
		}
	}
	if record.Rate.Value >= 5 {
		t.Fatalf("rate = %v", record.Rate.Value)
	}
}

func TestKeepsMonthlyProviderOutOfRollups(t *testing.T) {
	conn := fixtures.New(t)
	withMonthlyProvider(t, conn, latest())
	records := query(t, conn, "base", "EUR", "quotes", "USD", "from", d(addMonths(latest(), -2)), "to", d(latest()),
		"group", "month", "expand", "providers")
	for _, r := range records {
		for _, p := range r.Providers {
			if p.Key == "TST" {
				t.Fatal("TST contributes to a rollup")
			}
		}
	}
}

func TestKeepsMonthlyProviderOutOfMaterializedBlend(t *testing.T) {
	conn := fixtures.New(t)
	withMonthlyProvider(t, conn, latest())
	rebuildDaily(t, conn)
	var rate float64
	if err := conn.QueryRowContext(ctx, "SELECT rate FROM blended_rates WHERE quote = 'GBP' ORDER BY date DESC LIMIT 1").
		Scan(&rate); err != nil {
		t.Fatal(err)
	}
	if rate >= 5 {
		t.Fatalf("rate = %v", rate)
	}
}

func TestServesMonthlyProvidersOldValueByName(t *testing.T) {
	conn := fixtures.New(t)
	withMonthlyProvider(t, conn, latest().AddDate(0, 0, -40))
	record := query(t, conn, "providers", "TST", "base", "EUR", "quotes", "USD", "date", d(latest()))[0]
	if record.Rate.Value != 9.0 || record.Date != d(latest().AddDate(0, 0, -40)) {
		t.Fatalf("record = %+v", record)
	}
}

func TestStillForgetsDailyProviderAfterTwoWeeks(t *testing.T) {
	conn := fixtures.New(t)
	if r := query(t, conn, "providers", "ECB", "base", "EUR", "quotes", "USD", "date", d(latest().AddDate(0, 0, 30))); len(r) != 0 {
		t.Fatalf("records = %+v", r)
	}
}

// storedECB is the ECB rows on date, by base and quote.
func storedECB(t *testing.T, conn *sql.DB, date time.Time) map[[2]string]float64 {
	t.Helper()
	rows, err := conn.QueryContext(ctx, "SELECT base, quote, rate FROM rates WHERE provider = 'ECB' AND date = ?", d(date))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[[2]string]float64{}
	for rows.Next() {
		var b, q string
		var r float64
		if err := rows.Scan(&b, &q, &r); err != nil {
			t.Fatal(err)
		}
		out[[2]string{b, q}] = r
	}
	return out
}

func TestSingleProviderEchoesPublishedDigits(t *testing.T) {
	conn := fixtures.New(t)
	stored := storedECB(t, conn, latest())
	var records []Record
	for _, r := range query(t, conn, "providers", "ECB", "base", "EUR", "date", d(latest())) {
		if r.Quote != "EUR" {
			records = append(records, r)
		}
	}
	if len(records) != len(stored) {
		t.Fatalf("%d records, %d stored", len(records), len(stored))
	}
	for _, r := range records {
		if r.Rate.Value != stored[[2]string{"EUR", r.Quote}] {
			t.Errorf("%s = %v, stored %v", r.Quote, r.Rate.Value, stored[[2]string{"EUR", r.Quote}])
		}
	}
}

func TestSingleProviderCrossesThroughItsOwnBase(t *testing.T) {
	conn := fixtures.New(t)
	stored := storedECB(t, conn, latest())
	record := query(t, conn, "providers", "ECB", "base", "GBP", "quotes", "JPY", "date", d(latest()))[0]
	if want := roundValue(stored[[2]string{"EUR", "JPY"}] / stored[[2]string{"EUR", "GBP"}]); record.Rate.Value != want {
		t.Fatalf("rate = %v, want %v", record.Rate.Value, want)
	}
}

func TestSingleProviderKeepsRowsItCannotBridgeToUSD(t *testing.T) {
	conn := fixtures.New(t)
	insert(t, conn, "TST", latest(), "EUR", "GBP", 0.86)
	insert(t, conn, "TST", latest(), "EUR", "JPY", 160.0)
	byQuote := map[string]float64{}
	for _, r := range query(t, conn, "providers", "TST", "base", "GBP", "date", d(latest())) {
		byQuote[r.Quote] = r.Rate.Value
	}
	if len(byQuote) != 3 || byQuote["JPY"] != roundValue(160.0/0.86) || byQuote["EUR"] != roundValue(1/0.86) ||
		byQuote["GBP"] != 1.0 {
		t.Fatalf("rates = %v", byQuote)
	}
}

func TestSingleProviderKeepsOneRecordPerPair(t *testing.T) {
	conn := fixtures.New(t)
	date := latest()
	insert(t, conn, "TST", date.AddDate(0, 0, -1), "USD", "LTL", 2.8387)
	insert(t, conn, "TST", date.AddDate(0, 0, -1), "EUR", "LTL", 3.4528)
	insert(t, conn, "TST", date, "EUR", "USD", 1.2043)
	records := query(t, conn, "providers", "TST", "base", "USD", "quotes", "EUR", "date", d(date))
	if len(records) != 1 || records[0].Date != d(date) || records[0].Rate.Value != roundValue(1/1.2043) {
		t.Fatalf("records = %+v", records)
	}
}

func TestSingleProviderServesPeggedBase(t *testing.T) {
	conn := fixtures.New(t)
	insert(t, conn, "TST", latest(), "EUR", "AED", 4.0)
	insert(t, conn, "TST", latest(), "EUR", "USD", 1.09)
	records := query(t, conn, "providers", "TST", "base", "AED", "date", d(latest()))
	if r := find(records, "USD"); r == nil || r.Rate.Value != roundValue(1.09/4.0) {
		t.Fatalf("USD = %+v", r)
	}
	if r := find(records, "EUR"); r == nil || r.Rate.Value != roundValue(1/4.0) {
		t.Fatalf("EUR = %+v", r)
	}
}

func TestSingleProviderStillExpandsProviders(t *testing.T) {
	conn := fixtures.New(t)
	record := query(t, conn, "providers", "ECB", "base", "GBP", "quotes", "JPY", "date", d(latest()), "expand", "providers")[0]
	if len(record.Providers) != 1 {
		t.Fatalf("providers = %+v", record.Providers)
	}
	p := record.Providers[0]
	if p.Key != "ECB" || p.Date != d(latest()) || p.Rate != record.Rate {
		t.Fatalf("provider = %+v, record %+v", p, record)
	}
}

func TestSingleProviderEmitsOneRecordPerPublishedDate(t *testing.T) {
	conn := fixtures.New(t)
	from := fixtures.BusinessDay(5)
	records := query(t, conn, "providers", "ECB", "base", "GBP", "quotes", "JPY", "from", d(from), "to", d(latest()))
	published := count(t, conn, "SELECT count(DISTINCT date) FROM rates WHERE provider = 'ECB' AND date >= ? AND date <= ?",
		d(from), d(latest()))
	if got := len(uniq(dates(records))); got != published {
		t.Fatalf("%d dates, %d published", got, published)
	}
}

func TestRaisesOnInvalidDate(t *testing.T) {
	invalidQuery(t, fixtures.New(t), "date", "not-a-date")
}

func TestRaisesOnConflictingDateParams(t *testing.T) {
	invalidQuery(t, fixtures.New(t), "date", d(latest()), "from", d(latest().AddDate(0, 0, -30)))
}

func TestRaisesOnInvalidGroup(t *testing.T) {
	invalidQuery(t, fixtures.New(t), "group", "day")
}

func TestAcceptsValidGroup(t *testing.T) {
	conn := fixtures.New(t)
	if len(query(t, conn, "from", d(latest().AddDate(0, 0, -90)), "to", d(latest()), "group", "month")) == 0 {
		t.Fatal("no records")
	}
}

func TestFiltersByQuotes(t *testing.T) {
	conn := fixtures.New(t)
	var quotes []string
	for _, r := range query(t, conn, "quotes", "USD,GBP") {
		quotes = append(quotes, r.Quote)
	}
	quotes = uniq(quotes)
	slices.Sort(quotes)
	if !slices.Equal(quotes, []string{"GBP", "USD"}) {
		t.Fatalf("quotes = %v", quotes)
	}
}

func TestReturnsNothingBeforeDataset(t *testing.T) {
	if r := query(t, fixtures.New(t), "date", "1901-01-01"); len(r) != 0 {
		t.Fatalf("records = %+v", r)
	}
}

func TestRaisesOnInvalidCurrencies(t *testing.T) {
	conn := fixtures.New(t)
	invalidQuery(t, conn, "base", "FOO")
	invalidQuery(t, conn, "quotes", "USD,FOO")
	err := invalidQuery(t, conn, "base", "FOO", "quotes", "BAR")
	if !strings.Contains(err.Error(), "FOO") || !strings.Contains(err.Error(), "BAR") {
		t.Fatalf("message = %q", err)
	}
}

// Deadline enforcement.

func expired() Options { return Options{Deadline: time.Now()} }

func TestDeadlineRaisesBetweenChunksOfDailyRange(t *testing.T) {
	conn := fixtures.New(t)
	q := newQueryWith(t, conn, expired(), "from", d(latest().AddDate(0, 0, -30)), "to", d(latest()))
	if _, err := q.All(ctx); !isDeadline(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestDeadlineRaisesBetweenChunksOfRollupRange(t *testing.T) {
	conn := fixtures.New(t)
	q := newQueryWith(t, conn, expired(), "from", d(latest().AddDate(0, 0, -30)), "to", d(latest()), "group", "month")
	if _, err := q.All(ctx); !isDeadline(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestDeadlineDoesNotBoundSingleDates(t *testing.T) {
	conn := fixtures.New(t)
	q := newQueryWith(t, conn, expired(), "date", d(latest()), "quotes", "USD")
	if len(all(t, q)) == 0 {
		t.Fatal("no records")
	}
}

func TestCompletesRangesWithinDeadline(t *testing.T) {
	conn := fixtures.New(t)
	q := newQueryWith(t, conn, Options{Deadline: time.Now().Add(1000 * time.Second)},
		"from", d(latest().AddDate(0, 0, -30)), "to", d(latest()))
	if len(all(t, q)) == 0 {
		t.Fatal("no records")
	}
}

// Pins the check inside the chunk loop: a check hoisted to the loop entry would let a long compute run past its
// deadline.
func TestChecksDeadlineBeforeEveryChunk(t *testing.T) {
	conn := fixtures.New(t)
	q := newQuery(t, conn, "from", d(latest().AddDate(0, 0, -200)), "to", d(latest()))
	checks := 0
	q.checkHook = func() { checks++ }
	all(t, q)
	if checks <= 1 {
		t.Fatalf("checks = %d", checks)
	}
}

func TestStopsRangeWhenDeadlineExpiresMidCompute(t *testing.T) {
	conn := fixtures.New(t)
	q := newQuery(t, conn, "from", d(latest().AddDate(0, 0, -200)), "to", d(latest()))
	var emitted []Record
	err := q.Each(ctx, func(r Record) error {
		emitted = append(emitted, r)
		q.opts.Deadline = time.Unix(0, 0) // expire while the first chunk streams
		return nil
	})
	if !isDeadline(err) || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("err = %v", err)
	}
	if len(emitted) == 0 {
		t.Fatal("nothing emitted")
	}
}

// Heavy compute slots. providers= keeps the range on the live path regardless of the materialized table.

func heavyQuery(t *testing.T, conn *sql.DB, slots *heavyslots.Slots) *Query {
	t.Helper()
	return newQueryWith(t, conn, Options{Slots: slots}, "providers", "ECB",
		"from", d(latest().AddDate(0, 0, -200)), "to", d(latest()))
}

// paused runs q.Each in the background, stopped inside its first record until release is called. release(err) lets
// it go on (nil) or abandons it (an error from yield, as a client that went away), and waits for it to finish.
func paused(t *testing.T, q *Query) (release func(error) error) {
	t.Helper()
	first := make(chan struct{})
	resume := make(chan error)
	done := make(chan error)
	go func() {
		n := 0
		done <- q.Each(ctx, func(Record) error {
			n++
			if n == 1 {
				close(first)
				return <-resume
			}
			return nil
		})
	}()
	select {
	case <-first:
	case err := <-done:
		t.Fatalf("finished before the first record: %v", err)
	}
	return func(err error) error {
		resume <- err
		return <-done
	}
}

func TestRefusesHeavyRangeWhileSlotsHeldAndAdmitsOnceReleased(t *testing.T) {
	conn := fixtures.New(t)
	slots := heavyslots.New(1)
	release := paused(t, heavyQuery(t, conn, slots))
	if slots.Held() != 1 {
		t.Fatalf("held = %d", slots.Held())
	}
	if _, err := heavyQuery(t, conn, slots).All(ctx); !isBusy(err) {
		t.Fatalf("err = %v", err)
	}
	if err := release(nil); err != nil {
		t.Fatal(err)
	}
	if slots.Held() != 0 {
		t.Fatalf("held = %d", slots.Held())
	}
	if len(all(t, heavyQuery(t, conn, slots))) == 0 {
		t.Fatal("no records")
	}
}

func TestHeavyRangeFailsFastNamingRetryDelay(t *testing.T) {
	conn := fixtures.New(t)
	slots := heavyslots.New(1)
	slots.TryAcquire()
	_, err := heavyQuery(t, conn, slots).All(ctx)
	if !isBusy(err) || !strings.Contains(err.Error(), "30") {
		t.Fatalf("err = %v", err)
	}
}

func TestReleasesSlotAfterCompleteEnumeration(t *testing.T) {
	conn := fixtures.New(t)
	slots := heavyslots.New(1)
	if len(all(t, heavyQuery(t, conn, slots))) == 0 || slots.Held() != 0 {
		t.Fatalf("held = %d", slots.Held())
	}
}

func TestReleasesSlotWhenDeadlineExpiresMidCompute(t *testing.T) {
	conn := fixtures.New(t)
	slots := heavyslots.New(1)
	q := heavyQuery(t, conn, slots)
	err := q.Each(ctx, func(Record) error {
		q.opts.Deadline = time.Unix(0, 0)
		return nil
	})
	if !isDeadline(err) || slots.Held() != 0 {
		t.Fatalf("err = %v, held = %d", err, slots.Held())
	}
}

func TestReleasesSlotWhenComputeRaises(t *testing.T) {
	conn := fixtures.New(t)
	slots := heavyslots.New(1)
	q := heavyQuery(t, conn, slots)
	boom := errors.New("boom")
	q.emitHook = func() error { return boom }
	if _, err := q.All(ctx); !errors.Is(err, boom) || slots.Held() != 0 {
		t.Fatalf("err = %v, held = %d", err, slots.Held())
	}
}

// An enumeration abandoned mid-stream (client disconnect) has its slot returned through ReleaseSlot; a second call
// is a no-op, and so is the enumeration's own release when it unwinds.
func TestReleasesAbandonedEnumerationsSlotOnce(t *testing.T) {
	conn := fixtures.New(t)
	slots := heavyslots.New(2)
	slots.TryAcquire() // someone else's
	q := heavyQuery(t, conn, slots)
	release := paused(t, q)
	if slots.Held() != 2 {
		t.Fatalf("held = %d", slots.Held())
	}
	q.ReleaseSlot()
	q.ReleaseSlot()
	if slots.Held() != 1 {
		t.Fatalf("held = %d", slots.Held())
	}
	gone := errors.New("client went away")
	if err := release(gone); !errors.Is(err, gone) {
		t.Fatalf("err = %v", err)
	}
	if slots.Held() != 1 {
		t.Fatalf("held = %d after unwinding", slots.Held())
	}
}

func TestLeavesCheapShapesUntouchedWhileSlotsHeld(t *testing.T) {
	conn := fixtures.New(t)
	rebuildDaily(t, conn)
	slots := heavyslots.New(1)
	slots.TryAcquire()
	from, to := d(latest().AddDate(0, 0, -200)), d(latest())
	for _, kv := range [][]string{
		{"from", from, "to", to},
		{"from", from, "to", to, "group", "week"},
		{"from", from, "to", to, "group", "month", "providers", "ECB"},
		{},
		{"providers", "ECB"},
		{"date", to},
		{"date", to, "expand", "providers"},
	} {
		if len(all(t, newQueryWith(t, conn, Options{Slots: slots}, kv...))) == 0 {
			t.Errorf("%v: no records", kv)
		}
	}
	if slots.Held() != 1 {
		t.Fatalf("held = %d", slots.Held())
	}
}

// The daily range cap for live-path shapes. Validation is date arithmetic only, so fixed dates keep these
// deterministic.

const (
	capEnd       = "2026-01-15"
	atCapStart   = "2021-01-15"
	overCapStart = "2021-01-14"
)

func TestRangeCap(t *testing.T) {
	conn := fixtures.New(t)
	future := d(addMonths(today(), 120))
	for _, c := range []struct {
		name  string
		kv    []string
		allow bool
	}{
		{"rejects plain long ranges while not ready", []string{"from", overCapStart, "to", capEnd}, false},
		{"gives no quotes exemption while not ready", []string{"from", overCapStart, "to", capEnd, "quotes", "USD,GBP"}, false},
		{"rejects more than 5 providers", []string{"from", overCapStart, "to", capEnd,
			"providers", "ECB,BOC,BOJ,FED,SNB,BOE", "quotes", "USD"}, false},
		{"rejects providers= without quotes", []string{"from", overCapStart, "to", capEnd, "providers", "ECB,BOC"}, false},
		{"allows a single provider", []string{"from", overCapStart, "to", capEnd, "providers", "ECB"}, true},
		{"allows a single provider with many quotes", []string{"from", overCapStart, "to", capEnd, "providers", "ECB",
			"quotes", "USD,GBP,JPY,CHF,SEK,NOK"}, true},
		{"rejects provider-unbounded expand with few quotes", []string{"from", overCapStart, "to", capEnd,
			"expand", "providers", "quotes", "USD"}, false},
		{"allows bounded expand with few quotes", []string{"from", overCapStart, "to", capEnd, "expand", "providers",
			"providers", "ECB,BOC", "quotes", "USD"}, true},
		{"rejects more than 5 quotes", []string{"from", overCapStart, "to", capEnd, "providers", "ECB,BOC",
			"quotes", "USD,GBP,JPY,CHF,SEK,NOK"}, false},
		{"counts distinct quotes", []string{"from", overCapStart, "to", capEnd, "providers", "ECB,BOC",
			"quotes", "USD,USD,GBP,GBP,JPY,JPY"}, true},
		{"counts a future to only up to today", []string{"from", d(addMonths(today(), -12)), "to", future,
			"providers", "ECB,BOC"}, true},
		{"rejects a long past range regardless of a future to", []string{"from", d(addMonths(today(), -61)), "to", future,
			"providers", "ECB,BOC"}, false},
		{"allows 5 quotes", []string{"from", overCapStart, "to", capEnd, "providers", "ECB,BOC",
			"quotes", "USD,GBP,JPY,CHF,SEK"}, true},
		{"allows grouped ranges", []string{"from", overCapStart, "to", capEnd, "providers", "ECB,BOC", "group", "month"}, true},
		{"allows exactly 5 years", []string{"from", atCapStart, "to", capEnd, "providers", "ECB,BOC"}, true},
	} {
		_, err := New(ctx, conn, params(c.kv...), Options{Today: today()})
		switch {
		case c.allow && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case !c.allow && !IsValidation(err):
			t.Errorf("%s: err = %v", c.name, err)
		}
	}

	err := invalidQuery(t, conn, "from", overCapStart, "to", capEnd, "expand", "providers")
	for _, s := range []string{"quotes=", "group=week or group=month", "split the range"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("message %q lacks %q", err, s)
		}
	}
	if q := newQuery(t, conn, "date", "2001-01-15", "providers", "ECB"); q.Range() {
		t.Error("a single date is a range")
	}
	if ready(t, conn) {
		t.Fatal("table ready before a rebuild")
	}
}

func TestRangeCapLiftsOnceTableIsReady(t *testing.T) {
	conn := fixtures.New(t)
	rebuildDaily(t, conn)
	for _, kv := range [][]string{
		{"from", "1999-01-04", "to", capEnd},
		{"from", "1999-01-04", "to", capEnd, "quotes", "USD,GBP,JPY,CHF,SEK,NOK"},
	} {
		if !newQuery(t, conn, kv...).Range() {
			t.Errorf("%v is not a range", kv)
		}
	}
}

// Materialized blend dispatch. Deleting recent raw rows after the rebuild makes the two paths distinguishable: only
// the table still knows those dates. The oldest rows stay, so the table stays ready.

func deleteRecentRawRows(t *testing.T, conn *sql.DB) {
	exec(t, conn, "DELETE FROM rates WHERE date >= ? AND date <= ?", d(latest().AddDate(0, 0, -10)), d(latest()))
}

func TestServesPlainDailyRangesFromTable(t *testing.T) {
	conn := fixtures.New(t)
	rebuildDaily(t, conn)
	deleteRecentRawRows(t, conn)
	records := query(t, conn, "from", d(latest().AddDate(0, 0, -10)), "to", d(latest()))
	if slices.Max(dates(records)) != d(latest()) {
		t.Fatalf("newest = %s", slices.Max(dates(records)))
	}
}

func TestKeepsProvidersAndExpandRangesLive(t *testing.T) {
	conn := fixtures.New(t)
	rebuildDaily(t, conn)
	deleteRecentRawRows(t, conn)
	from, to := d(latest().AddDate(0, 0, -10)), d(latest())
	for _, kv := range [][]string{{"providers", "ECB"}, {"expand", "providers"}} {
		if containsString(dates(query(t, conn, append([]string{"from", from, "to", to}, kv...)...)), to) {
			t.Errorf("%v served %s", kv, to)
		}
	}
}

func TestFallsBackToLiveWhileTableEmpty(t *testing.T) {
	conn := fixtures.New(t)
	if n := count(t, conn, "SELECT count(*) FROM blended_rates"); n != 0 {
		t.Fatalf("%d stored", n)
	}
	if len(query(t, conn, "from", d(latest().AddDate(0, 0, -10)), "to", d(latest()))) == 0 {
		t.Fatal("no records")
	}
}

func TestFallsBackToLiveWhileTablePartial(t *testing.T) {
	conn := fixtures.New(t)
	if err := blend.RefreshDaily(ctx, conn, latest().AddDate(0, 0, -2), latest(), today()); err != nil {
		t.Fatal(err)
	}
	if count(t, conn, "SELECT count(*) FROM blended_rates") == 0 || ready(t, conn) {
		t.Fatal("table should be partial")
	}
	early := fixtures.BusinessDay(200)
	if len(query(t, conn, "from", d(early.AddDate(0, 0, -5)), "to", d(early))) == 0 {
		t.Fatal("no records")
	}
}

// Materialized blend dispatch for latest and single dates. A tampered stored row makes the paths distinguishable.

func tamper(t *testing.T, conn *sql.DB, quote string, date time.Time, rate float64) {
	exec(t, conn, "UPDATE blended_rates SET rate = ? WHERE quote = ? AND date = ?", rate, quote, d(date))
}

func TestServesLatestFromTable(t *testing.T) {
	conn := fixtures.New(t)
	rebuildDaily(t, conn)
	tamper(t, conn, "GBP", latest(), 999.0)
	gbp := find(query(t, conn, "base", "USD", "quotes", "GBP"), "GBP")
	if gbp == nil || gbp.Rate.Value != 999.0 || gbp.Date != d(latest()) {
		t.Fatalf("GBP = %+v", gbp)
	}
}

func TestServesSingleDatesFromTable(t *testing.T) {
	conn := fixtures.New(t)
	rebuildDaily(t, conn)
	date := fixtures.BusinessDay(30)
	tamper(t, conn, "GBP", date, 999.0)
	gbp := find(query(t, conn, "base", "USD", "quotes", "GBP", "date", d(date)), "GBP")
	if gbp == nil || gbp.Rate.Value != 999.0 {
		t.Fatalf("GBP = %+v", gbp)
	}
}

func TestKeepsProvidersAndExpandLatestLive(t *testing.T) {
	conn := fixtures.New(t)
	rebuildDaily(t, conn)
	tamper(t, conn, "GBP", latest(), 999.0)
	for _, r := range query(t, conn, "base", "USD", "quotes", "GBP", "providers", "ECB,BOC") {
		if r.Rate.Value == 999.0 {
			t.Fatal("providers= served the table")
		}
	}
	gbp := find(query(t, conn, "base", "USD", "quotes", "GBP", "expand", "providers"), "GBP")
	if gbp == nil || gbp.Rate.Value == 999.0 || !gbp.HasProviders {
		t.Fatalf("GBP = %+v", gbp)
	}
}

func TestFallsBackToLiveSnapshotWhileNotReady(t *testing.T) {
	conn := fixtures.New(t)
	if len(query(t, conn, "quotes", "GBP")) == 0 {
		t.Fatal("no records")
	}
}

// The correction #573 exists for: a carried-forward row freezes at the value blended on its own observation date
// instead of drifting as later days re-decay it against the batch clock.
func TestServesCanonicalAnchorDateValuesForCarriedQuotes(t *testing.T) {
	conn := fixtures.New(t)
	ecbLast, bocLast := fixtures.BusinessDay(8), fixtures.BusinessDay(4)
	exec(t, conn, "DELETE FROM rates WHERE provider = 'ECB' AND quote = 'GBP' AND date > ?", d(ecbLast))
	exec(t, conn, "DELETE FROM rates WHERE provider = 'BOC' AND quote = 'GBP' AND date > ?", d(bocLast))
	rebuildDaily(t, conn)

	var canonical float64
	if err := conn.QueryRowContext(ctx, "SELECT rate FROM blended_rates WHERE quote = 'GBP' AND date = ?", d(bocLast)).
		Scan(&canonical); err != nil {
		t.Fatal(err)
	}
	table := find(query(t, conn, "base", "USD", "quotes", "GBP"), "GBP")
	if table.Date != d(bocLast) || table.Rate.Value != roundValue(canonical) {
		t.Fatalf("table = %+v, canonical %v", table, canonical)
	}
	q := newQuery(t, conn, "base", "USD", "quotes", "GBP")
	q.ForceLive = true
	live := find(all(t, q), "GBP")
	if live.Date != d(bocLast) || live.Rate == table.Rate {
		t.Fatalf("live = %+v, table %+v", live, table)
	}
}

func TestServesPeggedBasesFromTableViaDerive(t *testing.T) {
	conn := fixtures.New(t)
	rebuildDaily(t, conn)
	tamper(t, conn, "GBP", latest(), 999.0)
	peg, _ := currency.FindPeg("AED")
	records := query(t, conn, "base", "AED", "quotes", "GBP,USD")
	if gbp := find(records, "GBP"); gbp.Rate.Value != roundValue(999.0/peg.Rate) {
		t.Fatalf("GBP = %+v", gbp)
	}
	if usd := find(records, "USD"); usd.Rate.Value != roundValue(1/peg.Rate) {
		t.Fatalf("USD = %+v", usd)
	}
}

func TestFailsWhenRebuildWipesTableMidRead(t *testing.T) {
	conn := fixtures.New(t)
	rebuildDaily(t, conn)
	q := newQuery(t, conn, "base", "USD", "quotes", "GBP")
	calls := 0
	orig := dailyReady
	dailyReady = func(context.Context, db.Querier) (bool, error) {
		calls++
		return calls == 1, nil
	}
	t.Cleanup(func() { dailyReady = orig })
	if _, err := q.All(ctx); !errors.Is(err, errRebuilt) {
		t.Fatalf("err = %v", err)
	}
}

// Latest used to blend a single-frame batch in the requested base while the same shape as a range blended through
// the pivot, so the two disagreed about the same data. One frame everywhere (#573).
func TestBlendsSingleFrameLiveBatchesThroughPivot(t *testing.T) {
	conn := fixtures.New(t)
	date := latest()
	exec(t, conn, "DELETE FROM rates")
	for _, p := range []struct {
		key      string
		usd, gbp float64
	}{{"AAA", 1.10, 0.90}, {"BBB", 1.20, 0.80}} {
		insert(t, conn, p.key, date, "EUR", "USD", p.usd)
		insert(t, conn, p.key, date, "EUR", "GBP", p.gbp)
	}
	shape := []string{"base", "EUR", "quotes", "GBP", "providers", "AAA,BBB"}
	latestGBP := find(query(t, conn, shape...), "GBP")
	rangeGBP := find(query(t, conn, append(shape, "from", d(date), "to", d(date))...), "GBP")
	if latestGBP.Rate != rangeGBP.Rate {
		t.Fatalf("latest %v, range %v", latestGBP.Rate, rangeGBP.Rate)
	}
}

// Single-day queries.

func TestLatestIncludesNextDayObservations(t *testing.T) {
	conn := fixtures.New(t)
	tomorrow, dayAfter := today().AddDate(0, 0, 1), today().AddDate(0, 0, 2)
	exec(t, conn, "DELETE FROM rates WHERE provider = 'ECB' AND base = 'EUR' AND quote = 'USD' AND date IN (?, ?)",
		d(tomorrow), d(dayAfter))
	insert(t, conn, "ECB", tomorrow, "EUR", "USD", 9.99)
	insert(t, conn, "ECB", dayAfter, "EUR", "USD", 8.88)
	usd := find(query(t, conn, "providers", "ECB", "quotes", "USD"), "USD")
	if usd == nil || usd.Base != "EUR" || usd.Date != d(tomorrow) || usd.Rate.Value != 9.99 {
		t.Fatalf("USD = %+v", usd)
	}
}

func TestExplicitDatesAndOpenRangesStopAtToday(t *testing.T) {
	conn := fixtures.New(t)
	tomorrow := today().AddDate(0, 0, 1)
	exec(t, conn, "DELETE FROM rates WHERE provider = 'ECB' AND base = 'EUR' AND quote = 'USD' AND date = ?", d(tomorrow))
	insert(t, conn, "ECB", tomorrow, "EUR", "USD", 9.99)
	for _, kv := range [][]string{{"date", d(today())}, {"from", d(today())}} {
		if containsString(dates(query(t, conn, append(kv, "providers", "ECB", "quotes", "USD")...)), d(tomorrow)) {
			t.Errorf("%v served tomorrow", kv)
		}
	}
}

func TestSnapsSilentDateToMostRecentPublication(t *testing.T) {
	conn := fixtures.New(t)
	sunday := fixtures.RecentSunday()
	if got := query(t, conn, "date", d(sunday))[0].Date; got != d(fixtures.PrecedingFriday(sunday)) {
		t.Fatalf("date = %s", got)
	}
}

func TestStampsEachRowWithItsObservationDate(t *testing.T) {
	conn := fixtures.New(t)
	stale := latest().AddDate(0, 0, -5)
	insert(t, conn, "ECB", stale, "EUR", "RON", 4.97)
	records := query(t, conn, "date", d(latest()))
	if ron := find(records, "RON"); ron == nil || ron.Date != d(stale) {
		t.Fatalf("RON = %+v", ron)
	}
	if usd := find(records, "USD"); usd.Date != d(latest()) {
		t.Fatalf("USD = %+v", usd)
	}
}

// Range queries.

func TestRangeEmitsPairsOnlyOnPublishedDays(t *testing.T) {
	conn := fixtures.New(t)
	monday := fixtures.GapBoundaryMonday(60)
	insert(t, conn, "ECB", monday, "EUR", "RON", 4.97)
	records := query(t, conn, "from", d(monday.AddDate(0, 0, -3)), "to", d(monday.AddDate(0, 0, 4)))
	for _, weekend := range []time.Time{monday.AddDate(0, 0, -2), monday.AddDate(0, 0, -1)} {
		if containsString(dates(records), d(weekend)) {
			t.Errorf("served %s", d(weekend))
		}
	}
	var ron []Record
	for _, r := range records {
		if r.Base == "EUR" && r.Quote == "RON" {
			ron = append(ron, r)
		}
	}
	if len(ron) != 1 || ron[0].Date != d(monday) {
		t.Fatalf("RON = %+v", ron)
	}
}

func TestRangeSurfacesSilentPairsRecentPublication(t *testing.T) {
	conn := fixtures.New(t)
	monday := fixtures.GapBoundaryMonday(60)
	pre, in := monday.AddDate(0, 0, -5), monday.AddDate(0, 0, 2)
	insert(t, conn, "ECB", pre, "EUR", "RON", 4.97)
	insert(t, conn, "ECB", in, "EUR", "RON", 4.95)
	var got []string
	for _, r := range query(t, conn, "from", d(monday), "to", d(monday.AddDate(0, 0, 5))) {
		if r.Base == "EUR" && r.Quote == "RON" {
			got = append(got, r.Date)
		}
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{d(pre), d(in)}) {
		t.Fatalf("RON dates = %v", got)
	}
}

func TestRangeSnapsBackWhenStartIsGloballySilent(t *testing.T) {
	conn := fixtures.New(t)
	monday := fixtures.GapBoundaryMonday(60)
	friday, saturday, sunday := monday.AddDate(0, 0, -3), monday.AddDate(0, 0, -2), monday.AddDate(0, 0, -1)
	partial := uniq(dates(query(t, conn, "from", d(sunday), "to", d(monday), "quotes", "USD")))
	if !containsString(partial, d(friday)) || !containsString(partial, d(monday)) {
		t.Fatalf("dates = %v", partial)
	}
	silent := uniq(dates(query(t, conn, "from", d(saturday), "to", d(sunday), "quotes", "USD")))
	if !slices.Equal(silent, []string{d(friday)}) {
		t.Fatalf("dates = %v", silent)
	}
}

func TestRangeCarriesSilentProvidersIntoBlend(t *testing.T) {
	conn := fixtures.New(t)
	date := latest()
	exec(t, conn, "DELETE FROM rates WHERE provider = 'BOC' AND date = ?", d(date))
	shape := []string{"from", d(date.AddDate(0, 0, -3)), "to", d(date), "quotes", "USD"}
	withBOC := query(t, conn, shape...)
	exec(t, conn, "DELETE FROM rates WHERE provider = 'BOC'")
	withoutBOC := query(t, conn, shape...)
	at := func(records []Record) *Record {
		for i := range records {
			if records[i].Date == d(date) {
				return &records[i]
			}
		}
		return nil
	}
	with, without := at(withBOC), at(withoutBOC)
	if with == nil || without == nil || with.Rate == without.Rate {
		t.Fatalf("with %+v, without %+v", with, without)
	}
}

func onDate(records []Record, date string) []Record {
	var out []Record
	for _, r := range records {
		if r.Date == date {
			out = append(out, r)
		}
	}
	return out
}

func TestSameRatesForDateQueriedAloneOrInRange(t *testing.T) {
	conn := fixtures.New(t)
	date := latest()
	exec(t, conn, "DELETE FROM rates WHERE provider = 'BOC' AND date = ?", d(date))
	single := query(t, conn, "date", d(date), "quotes", "USD")
	ranged := onDate(query(t, conn, "from", d(date.AddDate(0, 0, -3)), "to", d(date), "quotes", "USD"), d(date))
	if len(single) == 0 || len(ranged) == 0 || ranged[0].Rate != single[0].Rate {
		t.Fatalf("single %+v, range %+v", single, ranged)
	}
}

func TestSameRatesForDateWithProvidersScope(t *testing.T) {
	conn := fixtures.New(t)
	date := latest()
	single := query(t, conn, "date", d(date), "providers", "ECB", "quotes", "USD")
	ranged := onDate(query(t, conn, "from", d(date.AddDate(0, 0, -3)), "to", d(date), "providers", "ECB",
		"quotes", "USD"), d(date))
	if len(single) == 0 || len(ranged) == 0 || ranged[0].Rate != single[0].Rate {
		t.Fatalf("single %+v, range %+v", single, ranged)
	}
}

// expand=providers.

func TestExpandIsOmittedByDefault(t *testing.T) {
	records := query(t, fixtures.New(t), "date", d(latest()), "quotes", "USD")
	if len(records) == 0 || records[0].HasProviders {
		t.Fatalf("records = %+v", records)
	}
}

func TestExpandAddsProviderObjects(t *testing.T) {
	records := query(t, fixtures.New(t), "date", d(latest()), "quotes", "USD", "expand", "providers")
	if len(records) == 0 || len(records[0].Providers) == 0 {
		t.Fatalf("records = %+v", records)
	}
	p := records[0].Providers[0]
	if p.Key == "" || p.Date == "" || p.Excluded {
		t.Fatalf("provider = %+v", p)
	}
}

func TestExpandMarksAllProvidersExcludedOnPegSnappedRows(t *testing.T) {
	conn := fixtures.New(t)
	insert(t, conn, "ECB", latest(), "EUR", "AED", 3.97)
	records := query(t, conn, "date", d(latest()), "base", "USD", "quotes", "AED", "expand", "providers")
	if len(records) == 0 || records[0].Rate.Value != 3.6725 {
		t.Fatalf("records = %+v", records)
	}
	if len(records[0].Providers) == 0 {
		t.Fatal("no providers")
	}
	for _, p := range records[0].Providers {
		if !p.Excluded || p.Date == "" {
			t.Fatalf("provider = %+v", p)
		}
	}
}

func TestExpandWorksWithRollups(t *testing.T) {
	records := query(t, fixtures.New(t), "from", d(latest().AddDate(0, 0, -90)), "to", d(latest()), "group", "week",
		"quotes", "USD", "expand", "providers")
	if len(records) == 0 || len(records[0].Providers) == 0 || records[0].Providers[0].Key == "" {
		t.Fatalf("records = %+v", records)
	}
}

func TestExpandRaisesOnUnknownValue(t *testing.T) {
	invalidQuery(t, fixtures.New(t), "expand", "weights")
}

// Peg gap filling. BTN is pegged 1:1 to INR (since 1974), and ECB provides INR. TEST starts covering BTN recently,
// leaving older dates to the peg.

func withRecentBTN(t *testing.T) *sql.DB {
	conn := fixtures.New(t)
	cutoff := fixtures.BusinessDay(30)
	for date := latest(); !date.Before(cutoff); date = date.AddDate(0, 0, -1) {
		if wd := date.Weekday(); wd != time.Saturday && wd != time.Sunday {
			insert(t, conn, "TEST", date, "EUR", "BTN", 90.0)
		}
	}
	return conn
}

func TestPegFillsDatesBeforeProviderCoverage(t *testing.T) {
	records := query(t, withRecentBTN(t), "date", d(fixtures.BusinessDay(60)), "quotes", "BTN")
	if len(records) == 0 || records[0].Quote != "BTN" {
		t.Fatalf("records = %+v", records)
	}
}

func TestPegUsesProviderRatesWhenAvailable(t *testing.T) {
	records := query(t, withRecentBTN(t), "date", d(latest()), "quotes", "BTN")
	if len(records) == 0 || records[0].Quote != "BTN" {
		t.Fatalf("records = %+v", records)
	}
}

func TestSeveralProvidersWithPeggedBaseReturnNothing(t *testing.T) {
	conn := fixtures.New(t)
	if r := query(t, conn, "date", d(latest()), "providers", "ECB,BOC", "base", "AED", "quotes", "USD"); len(r) != 0 {
		t.Fatalf("records = %+v", r)
	}
	if r := query(t, conn, "from", d(latest().AddDate(0, 0, -5)), "to", d(latest()), "providers", "ECB,BOC",
		"base", "AED", "quotes", "USD"); len(r) != 0 {
		t.Fatalf("range records = %+v", r)
	}
}

// derive.

var deriveDate = time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

func row(quote string, rate float64, providers ...currency.Contribution) currency.Blended {
	return currency.Blended{Date: deriveDate, Base: "USD", Quote: quote, Rate: rate, Providers: providers}
}

func TestDeriveReturnsNothingWithoutTarget(t *testing.T) {
	if got := derive([]currency.Blended{row("EUR", 0.93)}, "GBP"); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
	if got := derive(nil, "GBP"); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestDeriveRebasesByDivisionAndAppendsTargetToPivot(t *testing.T) {
	rows := []currency.Blended{
		row("EUR", 0.93, currency.Contribution{Key: "ECB", Rate: 0.93}),
		row("GBP", 0.79, currency.Contribution{Key: "ECB", Rate: 0.79}),
	}
	result := derive(rows, "EUR")
	var gbp, usd currency.Blended
	for _, r := range result {
		switch r.Quote {
		case "GBP":
			gbp = r
		case "USD":
			usd = r
		case "EUR":
			t.Fatal("derive kept the target's own row")
		}
	}
	close := func(got, want float64) bool { return math.Abs(got-want) <= 0.001 }
	if gbp.Base != "EUR" || !close(gbp.Rate, 0.79/0.93) || !close(gbp.Providers[0].Rate, 0.79/0.93) {
		t.Fatalf("GBP = %+v", gbp)
	}
	if usd.Base != "EUR" || !close(usd.Rate, 1.0/0.93) || !close(usd.Providers[0].Rate, 1.0/0.93) {
		t.Fatalf("USD = %+v", usd)
	}
}

func TestDeriveDropsTargetRow(t *testing.T) {
	for _, r := range derive([]currency.Blended{row("EUR", 0.93), row("GBP", 0.79)}, "EUR") {
		if r.Quote == "EUR" {
			t.Fatal("target row kept")
		}
	}
}

func TestDeriveProducesExactReciprocals(t *testing.T) {
	rows := []currency.Blended{row("EUR", 0.93), row("GBP", 0.79)}
	quote := func(rs []currency.Blended, q string) float64 {
		for _, r := range rs {
			if r.Quote == q {
				return r.Rate
			}
		}
		t.Fatalf("no %s", q)
		return 0
	}
	if product := quote(derive(rows, "EUR"), "GBP") * quote(derive(rows, "GBP"), "EUR"); math.Abs(product-1) > 1e-12 {
		t.Fatalf("product = %v", product)
	}
}

// Native-precision passthrough. The witness is a stored rate whose native precision exceeds what magnitude-based
// rounding would emit, so rounding it would change it.

func nativeRow(t *testing.T, conn *sql.DB) (time.Time, float64) {
	t.Helper()
	rows, err := conn.QueryContext(ctx, "SELECT date, rate FROM rates WHERE provider = 'ECB' AND base = 'EUR' AND quote = 'PHP'")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var date time.Time
		var rate float64
		if err := rows.Scan(&date, &rate); err != nil {
			t.Fatal(err)
		}
		if roundValue(rate) != rate {
			return date, rate
		}
	}
	t.Fatal("no native-precision PHP row")
	return time.Time{}, 0
}

func TestPassthroughReturnsVerbatimStoredRate(t *testing.T) {
	conn := fixtures.New(t)
	date, rate := nativeRow(t, conn)
	records := query(t, conn, "date", d(date), "providers", "ECB", "base", "EUR", "quotes", "PHP")
	if records[0].Rate.Value != rate {
		t.Fatalf("rate = %v, stored %v", records[0].Rate.Value, rate)
	}
}

func TestPassthroughStillRoundsReciprocal(t *testing.T) {
	conn := fixtures.New(t)
	date, rate := nativeRow(t, conn)
	records := query(t, conn, "date", d(date), "providers", "ECB", "base", "PHP", "quotes", "EUR")
	if records[0].Rate.Value != roundValue(1.0/rate) {
		t.Fatalf("rate = %v", records[0].Rate.Value)
	}
}

func TestPassthroughStillRoundsBlends(t *testing.T) {
	conn := fixtures.New(t)
	date, _ := nativeRow(t, conn)
	rate := query(t, conn, "date", d(date), "base", "EUR", "quotes", "PHP")[0].Rate.Value
	if rate != roundValue(rate) {
		t.Fatalf("rate = %v", rate)
	}
}

func TestPassthroughInExpandedProviders(t *testing.T) {
	conn := fixtures.New(t)
	date, rate := nativeRow(t, conn)
	record := query(t, conn, "date", d(date), "providers", "ECB", "base", "EUR", "quotes", "PHP", "expand", "providers")[0]
	if record.Rate.Value != rate {
		t.Fatalf("rate = %v", record.Rate.Value)
	}
	for _, p := range record.Providers {
		if p.Key == "ECB" && p.Rate.Value != rate {
			t.Fatalf("ECB rate = %v", p.Rate.Value)
		}
	}
}

func TestPassthroughStillRoundsRollups(t *testing.T) {
	conn := fixtures.New(t)
	records := query(t, conn, "from", d(latest().AddDate(0, 0, -90)), "to", d(latest()), "group", "week",
		"providers", "ECB", "base", "EUR", "quotes", "PHP")
	if len(records) == 0 {
		t.Fatal("no records")
	}
	for _, r := range records {
		if r.Rate.Value != roundValue(r.Rate.Value) {
			t.Fatalf("rate = %v", r.Rate.Value)
		}
	}
}

// csv_filename.

func TestCSVFilename(t *testing.T) {
	conn := fixtures.New(t)
	for _, c := range []struct {
		kv   []string
		want string
	}{
		{nil, "EUR-rates_latest.csv"},
		{[]string{"base", "usd", "quotes", "eur", "from", "2024-01-01", "to", "2024-12-31"}, "USD-EUR_2024-01-01_2024-12-31.csv"},
		{[]string{"quotes", "USD,GBP", "date", "2024-05-01"}, "EUR-rates_2024-05-01.csv"},
		{[]string{"providers", "ecb", "date", "2024-05-01"}, "ECB_EUR-rates_2024-05-01.csv"},
		{[]string{"providers", "ECB,BOC", "date", "2024-05-01"}, "EUR-rates_2024-05-01.csv"},
		{[]string{"from", "2024-01-01"}, "EUR-rates_2024-01-01_" + d(today()) + ".csv"},
		{[]string{"from", "2024-01-01", "to", "2024-12-31", "group", "week"}, "EUR-rates_2024-01-01_2024-12-31_weekly.csv"},
		{[]string{"from", "2024-01-01", "to", "2024-12-31", "group", "month"}, "EUR-rates_2024-01-01_2024-12-31_monthly.csv"},
		{[]string{"providers", `x"; y`, "date", "2024-05-01"}, "XY_EUR-rates_2024-05-01.csv"},
	} {
		if got := newQuery(t, conn, c.kv...).CSVFilename(); got != c.want {
			t.Errorf("%v: %s, want %s", c.kv, got, c.want)
		}
	}
}
