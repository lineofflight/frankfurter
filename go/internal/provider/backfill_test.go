package provider

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/adapters/bis"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// fakeAdapter stands in for Class.new(Provider::Adapters::Adapter) in the Ruby
// spec.
type fakeAdapter struct {
	fetch         func(after, upto time.Time) ([]adapter.Rate, error)
	backfillRange int
	leadDays      int
	revises       bool
}

func (f *fakeAdapter) Fetch(_ context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	return f.fetch(after, upto)
}
func (f *fakeAdapter) BackfillRange() int { return f.backfillRange }
func (f *fakeAdapter) LeadDays() int      { return f.leadDays }
func (f *fakeAdapter) Revises() bool      { return f.revises }

func returning(rows ...adapter.Rate) *fakeAdapter {
	return &fakeAdapter{fetch: func(time.Time, time.Time) ([]adapter.Rate, error) {
		return append([]adapter.Rate(nil), rows...), nil
	}}
}

func rate(date time.Time, base, quote string, r float64) adapter.Rate {
	return adapter.Rate{Date: date, Base: base, Quote: quote, Rate: r}
}

// logRecorder is an slog.Handler that keeps every record with its attributes
// flattened to text.
type logRecorder struct {
	mu      *sync.Mutex
	records *[]logged
	attrs   []slog.Attr
}

type logged struct {
	Level   slog.Level
	Message string
	Attrs   map[string]string
}

func newLogRecorder() *logRecorder {
	return &logRecorder{mu: &sync.Mutex{}, records: &[]logged{}}
}

func (h *logRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (h *logRecorder) WithGroup(string) slog.Handler            { return h }
func (h *logRecorder) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &logRecorder{mu: h.mu, records: h.records, attrs: append(append([]slog.Attr(nil), h.attrs...), attrs...)}
}

func (h *logRecorder) Handle(_ context.Context, r slog.Record) error {
	l := logged{Level: r.Level, Message: r.Message, Attrs: map[string]string{}}
	for _, a := range h.attrs {
		l.Attrs[a.Key] = a.Value.String()
	}
	r.Attrs(func(a slog.Attr) bool {
		l.Attrs[a.Key] = a.Value.String()
		return true
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	*h.records = append(*h.records, l)
	return nil
}

func (h *logRecorder) at(level slog.Level) []logged {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []logged
	for _, l := range *h.records {
		if l.Level == level {
			out = append(out, l)
		}
	}
	return out
}

// events records blend and cache calls in order.
type events struct {
	calls   []string
	windows [][2]time.Time
	buckets []map[rates.Precision][]string
	write   func(ctx context.Context, q db.Querier, from, to time.Time) error
	purge   error
}

func (e *events) RefreshTx(ctx context.Context, q db.Querier, from, to time.Time) error {
	e.calls = append(e.calls, "refresh")
	e.windows = append(e.windows, [2]time.Time{from, to})
	if e.write != nil {
		return e.write(ctx, q, from, to)
	}
	return nil
}

func (e *events) RefreshRollupsTx(_ context.Context, _ db.Querier, buckets map[rates.Precision][]string) error {
	e.calls = append(e.calls, "rollups")
	e.buckets = append(e.buckets, buckets)
	return nil
}

func (e *events) PurgeDebounced(context.Context) error {
	e.calls = append(e.calls, "purge")
	return e.purge
}

func (e *events) count(call string) int {
	n := 0
	for _, c := range e.calls {
		if c == call {
			n++
		}
	}
	return n
}

type env struct {
	conn     *sql.DB
	in       *Ingester
	provider Provider
	events   *events
	log      *logRecorder
	today    time.Time
}

func newEnv(t *testing.T, key string, a adapter.Adapter) *env {
	t.Helper()
	conn := fixtures.New(t)
	p, err := Find(context.Background(), conn, key)
	if err != nil || p == nil {
		t.Fatalf("find %s: %v", key, err)
	}
	e := &env{conn: conn, provider: *p, events: &events{}, log: newLogRecorder(), today: fixtures.Today()}
	e.in = &Ingester{
		DB:      conn,
		Blend:   e.events,
		Cache:   e.events,
		Logger:  slog.New(e.log),
		Today:   func() time.Time { return e.today },
		Adapter: func(string) (adapter.Adapter, error) { return a, nil },
	}
	return e
}

func (e *env) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := e.conn.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func (e *env) count(t *testing.T, where string, args ...any) int {
	t.Helper()
	var n int
	if err := e.conn.QueryRow("SELECT count(*) FROM rates WHERE provider = ? AND "+where,
		append([]any{e.provider.Key}, args...)...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *env) rate(t *testing.T, where string, args ...any) float64 {
	t.Helper()
	var r float64
	if err := e.conn.QueryRow("SELECT rate FROM rates WHERE provider = ? AND "+where,
		append([]any{e.provider.Key}, args...)...).Scan(&r); err != nil {
		t.Fatal(err)
	}
	return r
}

func d(t time.Time) string { return db.FormatDate(t) }

// defaultAdapter is the spec's `adapter`: one EUR/USD row at importDate,
// recording each fetch's window.
func defaultAdapter(importDate time.Time, params *[][2]time.Time) *fakeAdapter {
	return &fakeAdapter{fetch: func(after, upto time.Time) ([]adapter.Rate, error) {
		if params != nil {
			*params = append(*params, [2]time.Time{after, upto})
		}
		return []adapter.Rate{rate(importDate, "EUR", "USD", 1.1)}, nil
	}}
}

func TestBackfillImportsFetchedRecords(t *testing.T) {
	today := fixtures.Today()
	e := newEnv(t, "BCB", defaultAdapter(today, nil))
	e.in.Backfill(context.Background(), e.provider)

	var base, quote string
	var r float64
	if err := e.conn.QueryRow("SELECT base, quote, rate FROM rates WHERE provider = 'BCB' AND date = ?", d(today)).
		Scan(&base, &quote, &r); err != nil {
		t.Fatal(err)
	}
	if base != "EUR" || quote != "USD" || r != 1.1 {
		t.Fatalf("got %s/%s %v", base, quote, r)
	}
}

func TestBackfillUpsertsWithoutDuplicating(t *testing.T) {
	today := fixtures.Today()
	e := newEnv(t, "BCB", defaultAdapter(today, nil))
	ctx := context.Background()
	e.in.Backfill(ctx, e.provider)
	e.in.BackfillAfter(ctx, e.provider, today.AddDate(0, 0, -1))

	if n := e.count(t, "date = ?", d(today)); n != 1 {
		t.Fatalf("got %d rows", n)
	}
}

func revisingEnv(t *testing.T, revises bool) *env {
	today := fixtures.Today()
	a := defaultAdapter(today, nil)
	a.revises = revises
	e := newEnv(t, "BCB", a)
	e.exec(t, "INSERT INTO rates (provider, date, base, quote, mid) VALUES ('BCB', ?, 'EUR', 'USD', 1.0)", d(today))
	e.in.BackfillAfter(context.Background(), e.provider, today.AddDate(0, 0, -1))
	return e
}

func TestBackfillWarnsWhenAFetchedValueDiffersFromTheStoredRow(t *testing.T) {
	e := revisingEnv(t, true)
	warns := e.log.at(slog.LevelWarn)
	if len(warns) != 1 {
		t.Fatalf("got %d warnings: %+v", len(warns), warns)
	}
	w := warns[0]
	if w.Message != "stored rates differ from source" || w.Attrs["provider"] != "BCB" || w.Attrs["count"] != "1" {
		t.Fatalf("got %+v", w)
	}
	if want := d(e.today) + " EUR/USD stored 1 fetched 1.1"; w.Attrs["detail"] != want {
		t.Fatalf("detail %q, want %q", w.Attrs["detail"], want)
	}
}

func TestBackfillKeepsTheStoredValueOfARevisedRow(t *testing.T) {
	e := revisingEnv(t, true)
	if r := e.rate(t, "date = ? AND quote = 'USD'", d(e.today)); r != 1.0 {
		t.Fatalf("got %v", r)
	}
}

func TestBackfillStaysQuietForAnAdapterThatDoesNotRevise(t *testing.T) {
	e := revisingEnv(t, false)
	if warns := e.log.at(slog.LevelWarn); len(warns) != 0 {
		t.Fatalf("got %+v", warns)
	}
}

func TestBackfillKeepsAForwardDatedRowForAnAdapterWithALead(t *testing.T) {
	ahead := fixtures.Today().AddDate(0, 0, 14)
	a := returning(rate(ahead, "EUR", "USD", 1.1))
	a.leadDays = 31
	e := newEnv(t, "BCB", a)
	e.in.Backfill(context.Background(), e.provider)

	if n := e.count(t, "date = ?", d(ahead)); n != 1 {
		t.Fatalf("got %d rows", n)
	}
}

func TestBackfillRetainsUnrecognisedCurrencyCodes(t *testing.T) {
	today := fixtures.Today()
	e := newEnv(t, "BCB", returning(rate(today, "EUR", "USD", 1.1), rate(today, "EUR", "SDR", 1.5)))
	e.in.Backfill(context.Background(), e.provider)

	if n := e.count(t, "quote = 'SDR'"); n != 1 {
		t.Fatalf("got %d rows", n)
	}
}

func TestBackfillExcludesNonPositiveRates(t *testing.T) {
	today := fixtures.Today()
	e := newEnv(t, "BCB", returning(rate(today, "EUR", "USD", 1.1), rate(today, "EUR", "GBP", 0),
		rate(today, "EUR", "JPY", -1)))
	e.in.Backfill(context.Background(), e.provider)

	for quote, want := range map[string]int{"USD": 1, "GBP": 0, "JPY": 0} {
		if n := e.count(t, "date = ? AND quote = ?", d(today), quote); n != want {
			t.Errorf("%s: got %d rows, want %d", quote, n, want)
		}
	}
}

func TestBackfillStripsFloatNoiseFromSynthesisedRates(t *testing.T) {
	today := fixtures.Today()
	e := newEnv(t, "BCB", returning(rate(today, "EUR", "USD", (181.5264+181.76)/2.0),
		rate(today, "EUR", "GBP", 744.92/100.0)))
	e.in.Backfill(context.Background(), e.provider)

	if r := e.rate(t, "quote = 'USD' AND date = ?", d(today)); r != 181.6432 {
		t.Errorf("USD: got %v", r)
	}
	if r := e.rate(t, "quote = 'GBP' AND date = ?", d(today)); r != 7.4492 {
		t.Errorf("GBP: got %v", r)
	}
}

func TestBackfillKeepsEveryDigitAProviderPublishes(t *testing.T) {
	today := fixtures.Today()
	e := newEnv(t, "BCB", returning(rate(today, "EUR", "USD", 0.0001234567891)))
	e.in.Backfill(context.Background(), e.provider)

	if r := e.rate(t, "quote = 'USD' AND date = ?", d(today)); r != 0.0001234567891 {
		t.Fatalf("got %v", r)
	}
}

func TestBackfillDropsRecordsDatedImplausiblyFarInTheFuture(t *testing.T) {
	today := fixtures.Today()
	e := newEnv(t, "BCB", returning(rate(today, "EUR", "USD", 1.1), rate(today.AddDate(0, 0, 365), "EUR", "GBP", 0.85)))
	e.in.Backfill(context.Background(), e.provider)

	if n := e.count(t, "quote = 'USD' AND date = ?", d(today)); n != 1 {
		t.Errorf("USD: got %d rows", n)
	}
	if n := e.count(t, "quote = 'GBP'"); n != 0 {
		t.Errorf("GBP: got %d rows", n)
	}
}

func TestBackfillKeepsRecordsWithinTheNearFutureGraceWindow(t *testing.T) {
	tomorrow := fixtures.Today().AddDate(0, 0, 1)
	e := newEnv(t, "BCB", returning(rate(tomorrow, "EUR", "USD", 1.1)))
	e.in.Backfill(context.Background(), e.provider)

	if n := e.count(t, "quote = 'USD' AND date = ?", d(tomorrow)); n != 1 {
		t.Fatalf("got %d rows", n)
	}
}

func TestBackfillRetainsRecordsOnOrAfterADefunctCurrencysTerminalDate(t *testing.T) {
	e := newEnv(t, "BCB", returning(
		// Retain the source's observations after BYR retired on 2016-07-01.
		rate(adapter.Date(2016, 7, 1), "EUR", "BYR", 22000),
		rate(adapter.Date(2017, 1, 1), "BYR", "USD", 0.00005),
		rate(adapter.Date(2016, 6, 30), "EUR", "BYR", 22000),
		rate(adapter.Date(2016, 7, 1), "EUR", "USD", 1.1),
	))
	e.in.Backfill(context.Background(), e.provider)

	for _, c := range []struct{ where, date string }{
		{"quote = 'BYR' AND date = ?", "2016-07-01"},
		{"base = 'BYR' AND date = ?", "2017-01-01"},
		{"quote = 'BYR' AND date = ?", "2016-06-30"},
		{"quote = 'USD' AND date = ?", "2016-07-01"},
	} {
		if n := e.count(t, c.where, c.date); n != 1 {
			t.Errorf("%s %s: got %d rows", c.where, c.date, n)
		}
	}
}

func TestBackfillLeavesRecordsForCodesNotInTheTerminalDateTable(t *testing.T) {
	today := fixtures.Today()
	e := newEnv(t, "BCB", returning(rate(today, "EUR", "USD", 1.1), rate(today, "EUR", "GBP", 0.85)))
	e.in.Backfill(context.Background(), e.provider)

	if n := e.count(t, "date = ?", d(today)); n != 2 {
		t.Fatalf("got %d rows", n)
	}
}

func TestBackfillIngestsXDR(t *testing.T) {
	today := fixtures.Today()
	e := newEnv(t, "BCB", returning(rate(today, "EUR", "USD", 1.1), rate(today, "EUR", "XDR", 0.8)))
	e.in.Backfill(context.Background(), e.provider)

	if n := e.count(t, "quote = 'XDR'"); n != 1 {
		t.Fatalf("got %d rows", n)
	}
}

func TestBackfillRequestsADebouncedCachePurgeWhenNewRatesAreInserted(t *testing.T) {
	e := newEnv(t, "BCB", defaultAdapter(fixtures.Today(), nil))
	e.in.Backfill(context.Background(), e.provider)

	if e.events.count("purge") != 1 {
		t.Fatalf("calls %v", e.events.calls)
	}
}

func TestBackfillRefreshesTheMaterializedBlendOverTheInsertedWindowBeforePurging(t *testing.T) {
	today := fixtures.Today()
	e := newEnv(t, "BCB", defaultAdapter(today, nil))
	e.in.Backfill(context.Background(), e.provider)

	if got := strings.Join(e.events.calls, ","); got != "rollups,refresh,purge" {
		t.Fatalf("calls %s", got)
	}
	w := e.events.windows[0]
	if !w[0].Equal(today) || !w[1].Equal(today.AddDate(0, 0, rates.LookbackDays)) {
		t.Fatalf("window %s..%s", d(w[0]), d(w[1]))
	}
	week, month := d(rates.Bucket(rates.Week, today)), d(rates.Bucket(rates.Month, today))
	b := e.events.buckets[0]
	if len(b[rates.Week]) != 1 || b[rates.Week][0] != week || len(b[rates.Month]) != 1 || b[rates.Month][0] != month {
		t.Fatalf("buckets %v, want week %s month %s", b, week, month)
	}
}

func TestBackfillWritesBlendedRowsForInsertedDates(t *testing.T) {
	today := fixtures.Today()
	e := newEnv(t, "BCB", defaultAdapter(today, nil))
	e.in.Blend = nil // the real materialized blend
	count := func() int {
		var n int
		if err := e.conn.QueryRow("SELECT count(*) FROM blended_rates WHERE date = ?", d(today)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(); n != 0 {
		t.Fatalf("%d blended rows before backfill", n)
	}
	e.in.Backfill(context.Background(), e.provider)

	if count() == 0 {
		t.Fatal("no blended rows")
	}
}

func TestBackfillRollsBackTheInsertWhenTheBlendRefreshFails(t *testing.T) {
	today := fixtures.Today()
	e := newEnv(t, "BCB", defaultAdapter(today, nil))
	e.events.write = func(context.Context, db.Querier, time.Time, time.Time) error { return errors.New("busy") }
	e.in.Backfill(context.Background(), e.provider)

	if n := e.count(t, "date = ?", d(today)); n != 0 {
		t.Fatalf("got %d rows", n)
	}
	if e.events.count("purge") != 0 {
		t.Fatalf("calls %v", e.events.calls)
	}
	if errs := e.log.at(slog.LevelError); len(errs) != 1 || !strings.Contains(errs[0].Attrs["error"], "busy") {
		t.Fatalf("errors %+v", errs)
	}
}

func TestBackfillImportsNonDailyObservationsWithoutRefreshingDailyBlends(t *testing.T) {
	for frequency, key := range map[string]string{"weekly": "JPC", "monthly": "BIS"} {
		t.Run(frequency, func(t *testing.T) {
			importDate := adapter.Date(2025, 1, 31)
			e := newEnv(t, key, defaultAdapter(importDate, nil))
			if e.provider.ObservationFrequency() != frequency {
				t.Fatalf("%s frequency %s", key, e.provider.ObservationFrequency())
			}
			e.in.Backfill(context.Background(), e.provider)

			if got := strings.Join(e.events.calls, ","); got != "purge" {
				t.Fatalf("calls %s", got)
			}
			if r := e.rate(t, "date = ?", d(importDate)); r != 1.1 {
				t.Errorf("rate %v", r)
			}
			for table, bucket := range map[string]string{"weekly_rates": "2025-01-29", "monthly_rates": "2025-01-01"} {
				var r float64
				if err := e.conn.QueryRow("SELECT rate FROM "+table+" WHERE provider = ? AND bucket_date = ?", key,
					bucket).Scan(&r); err != nil || r != 1.1 {
					t.Errorf("%s %s: %v, %v", table, bucket, r, err)
				}
			}
			rows, err := e.conn.Query("SELECT iso_code, start_date, end_date FROM currency_coverages "+
				"WHERE provider_key = ? ORDER BY iso_code", key)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var codes []string
			for rows.Next() {
				var code string
				var start, end db.NullDate
				if err := rows.Scan(&code, &start, &end); err != nil {
					t.Fatal(err)
				}
				codes = append(codes, code)
				if !start.Time.Equal(importDate) || !end.Time.Equal(importDate) {
					t.Errorf("%s coverage %s..%s", code, d(start.Time), d(end.Time))
				}
			}
			if got := strings.Join(codes, ","); got != "EUR,USD" {
				t.Errorf("coverages %s", got)
			}
		})
	}
}

func TestBackfillDoesNotRequestACachePurgeWhenNoNewRatesAreInserted(t *testing.T) {
	today := fixtures.Today()
	e := newEnv(t, "BCB", defaultAdapter(today, nil))
	ctx := context.Background()
	e.in.Backfill(ctx, e.provider)
	e.events.calls = nil
	e.in.BackfillAfter(ctx, e.provider, today.AddDate(0, 0, -1))

	if e.events.count("purge") != 0 {
		t.Fatalf("calls %v", e.events.calls)
	}
}

func TestBackfillSkipsWhenAlreadyUpToDate(t *testing.T) {
	called := false
	a := &fakeAdapter{fetch: func(time.Time, time.Time) ([]adapter.Rate, error) {
		called = true
		return nil, nil
	}}
	e := newEnv(t, "BCB", a)
	e.exec(t, "INSERT INTO rates (date, provider, base, quote, mid) VALUES (?, 'BCB', 'EUR', 'USD', 1.1)", d(e.today))
	e.in.Backfill(context.Background(), e.provider)

	if called {
		t.Fatal("fetched although up to date")
	}
}

func TestBackfillChunksWhenAdapterHasBackfillRange(t *testing.T) {
	today := fixtures.Today()
	since := today.AddDate(0, 0, -90)
	var params [][2]time.Time
	a := defaultAdapter(today, &params)
	a.backfillRange = 30
	e := newEnv(t, "BCB", a)
	e.exec(t, "INSERT INTO rates (date, provider, base, quote, mid) VALUES (?, 'BCB', 'EUR', 'USD', 1.0)", d(since))
	e.in.Backfill(context.Background(), e.provider)

	if len(params) != 4 {
		t.Fatalf("got %d fetches: %v", len(params), params)
	}
	if !params[0][0].Equal(since) || !params[0][1].Equal(since.AddDate(0, 0, 29)) {
		t.Errorf("first window %s..%s", d(params[0][0]), d(params[0][1]))
	}
	if !params[3][1].IsZero() {
		t.Errorf("last window upto %s, want open", d(params[3][1]))
	}
}

// dailyAdapter serves one EUR/USD row a day through today, from the day after
// after, or from after itself when inclusive (as LB's archive does).
func dailyAdapter(today time.Time, inclusive bool, params *[][2]time.Time) *fakeAdapter {
	return &fakeAdapter{fetch: func(after, upto time.Time) ([]adapter.Rate, error) {
		*params = append(*params, [2]time.Time{after, upto})
		from := after
		if !inclusive {
			from = after.AddDate(0, 0, 1)
		}
		if upto.IsZero() {
			upto = today
		}
		var out []adapter.Rate
		for d := from; !d.After(upto); d = d.AddDate(0, 0, 1) {
			out = append(out, rate(d, "EUR", "USD", 1.1))
		}
		return out, nil
	}}
}

// coverageEnv is a BCB environment with no stored rates and coverage_start ten
// days ago.
func coverageEnv(t *testing.T, a adapter.Adapter) (*env, time.Time) {
	t.Helper()
	e := newEnv(t, "BCB", a)
	start := e.today.AddDate(0, 0, -10)
	e.provider.CoverageStart = start
	e.exec(t, "DELETE FROM rates WHERE provider = 'BCB'")
	return e, start
}

func TestBackfillFetchesTheCoverageStartDayOnAFirstBackfill(t *testing.T) {
	var params [][2]time.Time
	e, start := coverageEnv(t, dailyAdapter(fixtures.Today(), false, &params))
	e.in.Backfill(context.Background(), e.provider)

	if len(params) != 1 || !params[0][0].Equal(start.AddDate(0, 0, -1)) {
		t.Fatalf("windows %v, want one after the day before coverage_start %s", params, d(start))
	}
	var first string
	if err := e.conn.QueryRow("SELECT min(date) FROM rates WHERE provider = 'BCB'").Scan(&first); err != nil {
		t.Fatal(err)
	}
	if first != d(start) {
		t.Errorf("first stored date %s, want coverage_start %s", first, d(start))
	}
}

func TestBackfillStoresNothingDatedBeforeCoverageStart(t *testing.T) {
	var params [][2]time.Time
	e, start := coverageEnv(t, dailyAdapter(fixtures.Today(), true, &params))
	// The full task passes coverage_start explicitly.
	e.in.BackfillAfter(context.Background(), e.provider, start)

	if n := e.count(t, "date < ?", d(start)); n != 0 {
		t.Errorf("stored %d rows before coverage_start", n)
	}
	if n := e.count(t, "date = ?", d(start)); n != 1 {
		t.Errorf("got %d rows on coverage_start, want 1", n)
	}
}

func TestBackfillStoresWhatAnEarlierExplicitCursorFetches(t *testing.T) {
	var params [][2]time.Time
	e, start := coverageEnv(t, dailyAdapter(fixtures.Today(), true, &params))
	e.in.BackfillAfter(context.Background(), e.provider, start.AddDate(0, 0, -3))

	if len(params) != 1 || !params[0][0].Equal(start.AddDate(0, 0, -3)) {
		t.Fatalf("windows %v, want one after the explicit cursor", params)
	}
	if n := e.count(t, "date < ?", d(start)); n != 3 {
		t.Errorf("got %d rows before coverage_start, want the 3 fetched", n)
	}
}

// walkingAdapter walks its own windows, as BIS does.
type walkingAdapter struct {
	fakeAdapter
	afters []time.Time
}

func (w *walkingAdapter) FetchEach(_ context.Context, after time.Time, yield func([]adapter.Rate) error) error {
	w.afters = append(w.afters, after)
	return yield([]adapter.Rate{rate(fixtures.Today(), "EUR", "USD", 1.1)})
}

func TestBackfillUsesAnAdaptersOwnFetchEach(t *testing.T) {
	w := &walkingAdapter{fakeAdapter: fakeAdapter{fetch: func(time.Time, time.Time) ([]adapter.Rate, error) {
		return nil, errors.New("package FetchEach used")
	}}}
	e := newEnv(t, "BCB", w)
	since := e.today.AddDate(0, 0, -5)
	e.exec(t, "INSERT INTO rates (date, provider, base, quote, mid) VALUES (?, 'BCB', 'EUR', 'USD', 1.0)", d(since))
	e.in.Backfill(context.Background(), e.provider)

	if len(w.afters) != 1 || !w.afters[0].Equal(since) {
		t.Fatalf("own FetchEach afters %v, want [%s]", w.afters, d(since))
	}
	if n := e.count(t, "date = ?", d(e.today)); n != 1 {
		t.Errorf("got %d rows from the own FetchEach, want 1", n)
	}
}

func TestBackfillRevisitsAYearForBIS(t *testing.T) {
	var queries []string
	a := bis.New(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		queries = append(queries, r.URL.RawQuery)
		body := "FREQ,REF_AREA,CURRENCY,COLLECTION,TIME_PERIOD,OBS_VALUE,UNIT_MULT\n"
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})})
	e := newEnv(t, "BIS", a)
	a.Now = func() time.Time { return e.today }
	e.exec(t, "DELETE FROM rates WHERE provider = 'BIS'")
	e.exec(t, "INSERT INTO rates (date, provider, base, quote, mid) VALUES ('2025-01-31', 'BIS', 'USD', 'JPY', 150)")
	e.in.Backfill(context.Background(), e.provider)

	if len(queries) == 0 || !strings.Contains(queries[0], "ge:2024-01") {
		t.Fatalf("queries %v, want the first from 2024-01 (a year before the newest stored 2025-01-31)", queries)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBackfillRefreshesCurrenciesAndCurrencyCoverages(t *testing.T) {
	e := newEnv(t, "BCB", defaultAdapter(fixtures.Today(), nil))
	e.exec(t, "DELETE FROM currencies")
	e.exec(t, "DELETE FROM currency_coverages")
	e.in.Backfill(context.Background(), e.provider)

	var coverages, usd, eur int
	e.conn.QueryRow("SELECT count(*) FROM currency_coverages WHERE provider_key = 'BCB'").Scan(&coverages)
	e.conn.QueryRow("SELECT count(*) FROM currencies WHERE iso_code = 'USD'").Scan(&usd)
	e.conn.QueryRow("SELECT count(*) FROM currencies WHERE iso_code = 'EUR'").Scan(&eur)
	if coverages == 0 || usd != 1 || eur != 1 {
		t.Fatalf("coverages %d, USD %d, EUR %d", coverages, usd, eur)
	}
}

func TestBackfillStoresPerProviderDateRangesInCoverages(t *testing.T) {
	today := fixtures.Today()
	e := newEnv(t, "BCB", defaultAdapter(today, nil))
	e.exec(t, "DELETE FROM currency_coverages")
	e.in.Backfill(context.Background(), e.provider)

	var start, end db.NullDate
	if err := e.conn.QueryRow("SELECT start_date, end_date FROM currency_coverages "+
		"WHERE provider_key = 'BCB' AND iso_code = 'USD'").Scan(&start, &end); err != nil {
		t.Fatal(err)
	}
	if !start.Time.Equal(today) || !end.Time.Equal(today) {
		t.Fatalf("coverage %s..%s", d(start.Time), d(end.Time))
	}
}

func TestBackfillContainsAnyAdapterError(t *testing.T) {
	e := newEnv(t, "BCB", &fakeAdapter{fetch: func(time.Time, time.Time) ([]adapter.Rate, error) {
		return nil, errors.New("boom")
	}})
	e.in.Backfill(context.Background(), e.provider)

	errs := e.log.at(slog.LevelError)
	if len(errs) != 1 || errs[0].Message != "backfill failed, skipping" || errs[0].Attrs["provider"] != "BCB" ||
		errs[0].Attrs["error"] != "boom" {
		t.Fatalf("got %+v", errs)
	}
	if n := e.count(t, "date = ?", d(e.today)); n != 0 {
		t.Fatalf("got %d rows", n)
	}
}

func TestBackfillLogsAMissingAdapter(t *testing.T) {
	e := newEnv(t, "BCB", nil)
	e.in.Adapter = nil
	e.in.Backfill(context.Background(), e.provider)

	errs := e.log.at(slog.LevelError)
	if len(errs) != 1 || errs[0].Attrs["error"] != "no adapter registered for BCB" {
		t.Fatalf("got %+v", errs)
	}
}

// await waits for ch to close and gives up after five seconds with an error
// naming what never happened.
func await(ch <-chan struct{}, what string) error {
	select {
	case <-ch:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New(what)
	}
}

// overlapBlend runs inside each backfill's write transaction. It counts the
// transactions open at once and holds the first open until the other backfill
// has fetched, then well past the test's busy timeout.
type overlapBlend struct {
	calls, active, peak atomic.Int32
	writing, fetched    chan struct{}
}

func (b *overlapBlend) RefreshTx(context.Context, db.Querier, time.Time, time.Time) error {
	n := b.active.Add(1)
	defer b.active.Add(-1)
	for p := b.peak.Load(); n > p && !b.peak.CompareAndSwap(p, n); p = b.peak.Load() {
	}
	if b.calls.Add(1) > 1 {
		return nil
	}
	close(b.writing)
	if err := await(b.fetched, "BNR never fetched"); err != nil {
		return err
	}
	time.Sleep(250 * time.Millisecond)
	return nil
}

func (b *overlapBlend) RefreshRollupsTx(context.Context, db.Querier, map[rates.Precision][]string) error {
	return nil
}

// Concurrent backfills fetch side by side and write one at a time. BCB's fetch
// is still out when BNR's starts, and BNR's completes while BCB's write
// transaction is open. BNR then queues for the write lock instead of running
// into SQLite's, whose busy timeout here is far shorter than BCB's
// transaction.
func TestConcurrentBackfillsOverlapFetchesAndSerializeWrites(t *testing.T) {
	t.Setenv("SQLITE_BUSY_TIMEOUT", "50")
	conn := fixtures.New(t)
	today := fixtures.Today()
	blend := &overlapBlend{writing: make(chan struct{}), fetched: make(chan struct{})}
	fetching := make(chan struct{})
	adapters := map[string]adapter.Adapter{
		"BCB": &fakeAdapter{fetch: func(time.Time, time.Time) ([]adapter.Rate, error) {
			if err := await(fetching, "BNR never started fetching"); err != nil {
				return nil, err
			}
			return []adapter.Rate{rate(today, "EUR", "USD", 1.1)}, nil
		}},
		"BNR": &fakeAdapter{fetch: func(time.Time, time.Time) ([]adapter.Rate, error) {
			close(fetching)
			if err := await(blend.writing, "BCB never started writing"); err != nil {
				return nil, err
			}
			close(blend.fetched)
			return []adapter.Rate{rate(today, "EUR", "RON", 4.97)}, nil
		}},
	}
	log := newLogRecorder()
	in := &Ingester{
		DB:      conn,
		Blend:   blend,
		Logger:  slog.New(log),
		Today:   func() time.Time { return today },
		Adapter: func(key string) (adapter.Adapter, error) { return adapters[key], nil },
	}

	var wg sync.WaitGroup
	for _, key := range []string{"BCB", "BNR"} {
		p, err := Find(context.Background(), conn, key)
		if err != nil || p == nil {
			t.Fatalf("find %s: %v", key, err)
		}
		wg.Go(func() { in.BackfillAfter(context.Background(), *p, today.AddDate(0, 0, -1)) })
	}
	wg.Wait()

	if errs := log.at(slog.LevelError); len(errs) != 0 {
		t.Fatalf("errors %+v", errs)
	}
	if calls, peak := blend.calls.Load(), blend.peak.Load(); calls != 2 || peak != 1 {
		t.Fatalf("%d write transactions, %d open at once", calls, peak)
	}
	var n int
	if err := conn.QueryRow("SELECT count(*) FROM rates WHERE provider IN ('BCB', 'BNR') AND date = ?", d(today)).
		Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("got %d rows", n)
	}
}
