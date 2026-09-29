package schedule

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/adhocore/gronx"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/blend"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
	"github.com/lineofflight/frankfurter/go/internal/provider"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// timer is one registration, as the Ruby spec's stub scheduler records them.
type timer struct {
	method, name, expr string
	delay              time.Duration
	opts               Options
	fn                 Func
}

type recorder struct{ timers []timer }

func (r *recorder) In(name string, delay time.Duration, fn Func) {
	r.timers = append(r.timers, timer{method: "in", name: name, delay: delay, fn: fn})
}

func (r *recorder) Every(name string, interval time.Duration, opts Options, fn Func) {
	r.timers = append(r.timers, timer{method: "every", name: name, delay: interval, opts: opts, fn: fn})
}

func (r *recorder) Cron(name, expr string, opts Options, fn Func) error {
	r.timers = append(r.timers, timer{method: "cron", name: name, expr: expr, opts: opts, fn: fn})
	return nil
}

func (r *recorder) find(method, name string) (timer, bool) {
	for _, t := range r.timers {
		if t.method == method && t.name == name {
			return t, true
		}
	}
	return timer{}, false
}

func seededProviders(t *testing.T) []provider.Provider {
	t.Helper()
	ps, err := provider.All(context.Background(), fixtures.New(t))
	if err != nil || len(ps) == 0 {
		t.Fatalf("providers: %d, %v", len(ps), err)
	}
	return ps
}

func TestDryRunSchedulesProvidersAtStartupAndConfiguredOnesWithValidCron(t *testing.T) {
	providers := seededProviders(t)
	var out bytes.Buffer
	if err := DryRun(&out, providers); err != nil {
		t.Fatal(err)
	}
	var startup, crons []string
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		switch {
		case strings.HasPrefix(line, "startup:"):
			startup = append(startup, line)
		case strings.HasPrefix(line, "cron:"):
			crons = append(crons, line)
		}
	}
	scheduled := 0
	for _, p := range providers {
		if p.PublishSchedule != "" {
			scheduled++
		}
	}
	if len(startup) != len(providers) || len(crons) != scheduled {
		t.Fatalf("startup %d (want %d), cron %d (want %d)", len(startup), len(providers), len(crons), scheduled)
	}
	re := regexp.MustCompile(`^cron: (.+) backfill\[[a-z]+\]$`)
	for _, line := range crons {
		m := re.FindStringSubmatch(line)
		if m == nil || !gronx.IsValid(m[1]) {
			t.Errorf("invalid cron line %q", line)
		}
	}
}

func TestSetupSchedulesStartupBackfillsWithANumericStagger(t *testing.T) {
	providers := seededProviders(t)[:5]
	var backfilled []string
	r := &recorder{}
	err := Setup(r, Deps{
		Providers: providers,
		Backfill:  func(_ context.Context, p provider.Provider) { backfilled = append(backfilled, p.Key) },
		Shuffle:   slices.Reverse[[]provider.Provider],
	})
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	i := 0
	for _, tm := range r.timers {
		if tm.method != "in" {
			continue
		}
		if tm.delay != time.Duration(i)*2*time.Second {
			t.Errorf("startup %d delay %v", i, tm.delay)
		}
		if err := tm.fn(context.Background(), &Job{}); err != nil {
			t.Fatal(err)
		}
		want = append(want, providers[len(providers)-1-i].Key)
		i++
	}
	if i != len(providers) || !slices.Equal(backfilled, want) {
		t.Fatalf("backfilled %v, want %v", backfilled, want)
	}
}

func TestSetupSchedulesEachProviderOnItsPublishSchedule(t *testing.T) {
	providers := []provider.Provider{
		{Key: "A", PublishSchedule: "*/30 14-16 * * 1-5"},
		{Key: "B"},
		{Key: "C", PublishSchedule: "0 12 1-10 1,4,7,10 *"},
	}
	var backfilled []string
	r := &recorder{}
	if err := Setup(r, Deps{
		Providers: providers,
		Backfill:  func(_ context.Context, p provider.Provider) { backfilled = append(backfilled, p.Key) },
	}); err != nil {
		t.Fatal(err)
	}
	var exprs []string
	for _, tm := range r.timers {
		if tm.method != "cron" {
			continue
		}
		if !tm.opts.NoOverlap {
			t.Errorf("%s may overlap", tm.name)
		}
		exprs = append(exprs, tm.expr)
		tm.fn(context.Background(), &Job{})
	}
	if !slices.Equal(exprs, []string{"*/30 14-16 * * 1-5", "0 12 1-10 1,4,7,10 *"}) || !slices.Equal(backfilled, []string{"A", "C"}) {
		t.Fatalf("crons %v, backfilled %v", exprs, backfilled)
	}
}

type fakeCache struct{ debounced, pending int }

func (c *fakeCache) PurgeDebounced(context.Context) error { c.debounced++; return nil }
func (c *fakeCache) PurgePending(context.Context) error   { c.pending++; return nil }

// fakeBlend stands in for BlendedRate and the grouped rollup models.
type fakeBlend struct {
	ready     bool
	rebuild   func() error
	populate  map[rates.Precision]func() (int, error)
	populated map[rates.Precision]bool
	refreshed [][2]time.Time
	calls     []string
}

func newFakeBlend() *fakeBlend {
	return &fakeBlend{ready: true, populated: map[rates.Precision]bool{}}
}

func (b *fakeBlend) Refresh(_ context.Context, from, to time.Time) error {
	b.calls = append(b.calls, "refresh")
	b.refreshed = append(b.refreshed, [2]time.Time{from, to})
	return nil
}

func (b *fakeBlend) Ready(context.Context) (bool, error) { return b.ready, nil }

func (b *fakeBlend) Rebuild(context.Context) error {
	if b.rebuild != nil {
		return b.rebuild()
	}
	b.ready = true
	return nil
}

func (b *fakeBlend) Populate(_ context.Context, p rates.Precision) (int, error) {
	if f := b.populate[p]; f != nil {
		return f()
	}
	if b.populated[p] {
		return 0, nil
	}
	b.populated[p] = true
	return 7, nil
}

func populationTimer(t *testing.T, b Blend, c Cache) Func {
	t.Helper()
	r := &recorder{}
	if err := Setup(r, Deps{Blend: b, Cache: c, Backfill: func(context.Context, provider.Provider) {}}); err != nil {
		t.Fatal(err)
	}
	var found *timer
	for i, tm := range r.timers {
		if tm.method == "every" && tm.opts.FirstIn == 30*time.Second {
			found = &r.timers[i]
		}
	}
	if found == nil {
		t.Fatal("population must retry independently of provider startup timers")
	}
	if !found.opts.NoOverlap || found.delay != 5*time.Minute {
		t.Fatalf("population timer %+v", *found)
	}
	return found.fn
}

// readyBlend is the real materialized blend over the spec fixture, with the daily blend already built.
func readyBlend(t *testing.T) (*sql.DB, provider.Materialized) {
	t.Helper()
	conn := fixtures.New(t)
	m := provider.Materialized{DB: conn, Today: fixtures.Today}
	if err := m.Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	return conn, m
}

func rollupReady(t *testing.T, conn *sql.DB, r blend.Rollup) bool {
	t.Helper()
	ok, err := r.Ready(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// failingMonthly fails BlendedMonthlyRate.populate while fail is set.
type failingMonthly struct {
	provider.Materialized
	fail bool
}

func (f *failingMonthly) Populate(ctx context.Context, p rates.Precision) (int, error) {
	if f.fail && p == rates.Month {
		return 0, errors.New("database is busy")
	}
	return f.Materialized.Populate(ctx, p)
}

func TestPopulationBuildsGroupedTablesEvenWhenTheDailyBlendIsReady(t *testing.T) {
	conn, m := readyBlend(t)
	job := &Job{}
	if err := populationTimer(t, m, &fakeCache{})(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if !rollupReady(t, conn, blend.Weekly) || !rollupReady(t, conn, blend.Monthly) || !job.unscheduled.Load() {
		t.Fatalf("weekly ready %v, monthly ready %v, unscheduled %v", rollupReady(t, conn, blend.Weekly),
			rollupReady(t, conn, blend.Monthly), job.unscheduled.Load())
	}
}

func TestPopulationRetriesAFailedPopulationAndStopsOnlyAfterBothGroupedBuildsFinish(t *testing.T) {
	conn, m := readyBlend(t)
	b := &failingMonthly{Materialized: m, fail: true}
	timer := populationTimer(t, b, &fakeCache{})
	job := &Job{}
	if err := timer(context.Background(), job); err == nil {
		t.Fatal("want the population error")
	}
	var monthly int
	if err := conn.QueryRow("SELECT count(*) FROM " + blend.Monthly.Table).Scan(&monthly); err != nil {
		t.Fatal(err)
	}
	if job.unscheduled.Load() || !rollupReady(t, conn, blend.Weekly) || monthly != 0 {
		t.Fatalf("after failure: unscheduled %v, weekly ready %v, monthly rows %d", job.unscheduled.Load(),
			rollupReady(t, conn, blend.Weekly), monthly)
	}

	b.fail = false
	if err := timer(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if !job.unscheduled.Load() || !rollupReady(t, conn, blend.Monthly) {
		t.Fatalf("after retry: unscheduled %v, monthly ready %v", job.unscheduled.Load(),
			rollupReady(t, conn, blend.Monthly))
	}
}

func TestPopulationPurgesWhenADailyRebuildBecomesReadyBeforeItsFinalCleanupFails(t *testing.T) {
	b := newFakeBlend()
	b.ready = false
	b.rebuild = func() error {
		b.ready = true
		return errors.New("final cleanup failed")
	}
	c := &fakeCache{}
	timer := populationTimer(t, b, c)
	job := &Job{}
	if err := timer(context.Background(), job); err == nil {
		t.Fatal("want the rebuild error")
	}
	if c.debounced != 1 || job.unscheduled.Load() {
		t.Fatalf("purges %d, unscheduled %v", c.debounced, job.unscheduled.Load())
	}

	zero := func() (int, error) { return 0, nil }
	b.populate = map[rates.Precision]func() (int, error){rates.Week: zero, rates.Month: zero}
	if err := timer(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if c.debounced != 1 || !job.unscheduled.Load() {
		t.Fatalf("purges %d, unscheduled %v", c.debounced, job.unscheduled.Load())
	}
}

func TestMidnightJobReblendsTheTrailingWindowThenPurges(t *testing.T) {
	b := newFakeBlend()
	c := &fakeCache{}
	today := adapter.Date(2026, 4, 20)
	r := &recorder{}
	if err := Setup(r, Deps{Blend: b, Cache: c, Today: func() time.Time { return today }}); err != nil {
		t.Fatal(err)
	}
	tm, ok := r.find("cron", "midnight blend")
	if !ok || tm.expr != "0 0 * * *" {
		t.Fatalf("midnight job %+v", tm)
	}
	if err := tm.fn(context.Background(), &Job{}); err != nil {
		t.Fatal(err)
	}
	if len(b.refreshed) != 1 || !b.refreshed[0][0].Equal(adapter.Date(2026, 4, 19)) ||
		!b.refreshed[0][1].Equal(adapter.Date(2026, 4, 21)) || c.debounced != 1 {
		t.Fatalf("refreshed %v, purges %d", b.refreshed, c.debounced)
	}
}

func TestPurgePendingRunsEveryMinute(t *testing.T) {
	c := &fakeCache{}
	r := &recorder{}
	if err := Setup(r, Deps{Cache: c}); err != nil {
		t.Fatal(err)
	}
	tm, ok := r.find("every", "purge pending")
	if !ok || tm.delay != time.Minute {
		t.Fatalf("purge job %+v", tm)
	}
	tm.fn(context.Background(), &Job{})
	if c.pending != 1 {
		t.Fatalf("pending purges %d", c.pending)
	}
}

func TestSetupLeavesOutBlendAndCacheJobsWithoutThem(t *testing.T) {
	r := &recorder{}
	if err := Setup(r, Deps{Providers: []provider.Provider{{Key: "A"}}}); err != nil {
		t.Fatal(err)
	}
	if len(r.timers) != 1 || r.timers[0].method != "in" {
		t.Fatalf("timers %+v", r.timers)
	}
}
