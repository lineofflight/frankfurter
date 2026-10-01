package provider

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// Blend is the materialized blend (Materialized in production). Backfill calls
// it inside its write transaction, so implementations must run on q and not
// open a transaction of their own.
type Blend interface {
	// RefreshTx recomputes stored daily blends for every anchor date in [from,
	// to] (BlendedRate.refresh).
	RefreshTx(ctx context.Context, q db.Querier, from, to time.Time) error
	// RefreshRollupsTx recomputes the blended weekly and monthly buckets that
	// rates.RefreshRollups touched (BlendedWeeklyRate.refresh and
	// BlendedMonthlyRate.refresh).
	RefreshRollupsTx(ctx context.Context, q db.Querier, buckets map[rates.Precision][]string) error
}

// Cache is the CDN cache, owned by the cache step.
type Cache interface {
	// PurgeDebounced is Cache.purge_debounced: purge now, or mark a purge
	// pending inside the debounce window.
	PurgeDebounced(ctx context.Context) error
}

// Ingester runs backfills: it fetches through each provider's registered
// adapter, validates and normalises the rows, stores them and refreshes
// everything derived from them. Concurrent backfills should share one Ingester
// so their writes queue on its lock (see writes). It must not be copied after
// first use.
type Ingester struct {
	DB *sql.DB

	// Client is handed to registered adapter constructors. Nil means
	// adapter.NewClient().
	Client *http.Client

	// Blend defaults to Materialized. Cache is skipped when nil.
	Blend Blend
	Cache Cache

	// Logger defaults to slog.Default().
	Logger *slog.Logger

	// Today defaults to rates.Today.
	Today func() time.Time

	// Adapter resolves a provider key to its adapter. Nil means the adapter
	// registry; tests swap in fakes.
	Adapter func(key string) (adapter.Adapter, error)

	// writes is held around every write a backfill makes. SQLite admits one
	// writer at a time anyway; queueing here instead of on BEGIN IMMEDIATE
	// means a long batch cannot push another backfill past the busy timeout.
	// Fetches and reads stay outside it, so network waits overlap.
	writes sync.Mutex
}

func (in *Ingester) logger() *slog.Logger {
	if in.Logger != nil {
		return in.Logger
	}
	return slog.Default()
}

func (in *Ingester) today() time.Time {
	if in.Today != nil {
		return in.Today()
	}
	return rates.Today()
}

func (in *Ingester) blend() Blend {
	if in.Blend != nil {
		return in.Blend
	}
	return Materialized{DB: in.DB, Today: in.today}
}

func (in *Ingester) adapter(key string) (adapter.Adapter, error) {
	if in.Adapter != nil {
		return in.Adapter(key)
	}
	return Lookup(key, in.Client)
}

// Lookup builds the adapter registered under key with client
// (adapter.NewClient() when nil). The binary must import internal/adapters/all
// for every adapter to be registered.
func Lookup(key string, client *http.Client) (adapter.Adapter, error) {
	build, ok := adapter.Lookup(key)
	if !ok {
		return nil, fmt.Errorf("no adapter registered for %s", key)
	}
	if client == nil {
		client = adapter.NewClient()
	}
	return build(client), nil
}

// Backfill is Provider#backfill with its default cursor: after the newest
// stored rate, or from coverage_start when there is none, or from the source's
// start when neither exists.
func (in *Ingester) Backfill(ctx context.Context, p Provider) {
	last, err := p.LastSynced(ctx, in.DB)
	if err != nil {
		in.logger().Error("backfill failed, skipping", "provider", p.Key, "error", err)
		return
	}
	if last.IsZero() {
		last = p.CoverageStart
	}
	in.BackfillAfter(ctx, p, last)
}

// BackfillAfter fetches the provider's rows dated after `after` (zero: from the
// start) and stores them. An after equal to coverage_start backfills from
// coverage_start itself. Each fetched batch commits on its own. Failures are
// logged and end the run, as in Ruby, which rescues and moves on to the next
// provider.
func (in *Ingester) BackfillAfter(ctx context.Context, p Provider, after time.Time) {
	log := in.logger().With("provider", p.Key)
	if err := in.backfill(ctx, p, after, log); err != nil {
		log.Error("backfill failed, skipping", "error", err)
	}
}

func (in *Ingester) backfill(ctx context.Context, p Provider, after time.Time, log *slog.Logger) error {
	today := in.today()
	if !after.IsZero() && !after.Before(today) {
		log.Info("up to date")
		return nil
	}
	from := "start"
	if !after.IsZero() {
		from = db.FormatDate(after)
	}
	log.Info("backfilling", "from", from)

	// Many adapters read after as exclusive, but coverage_start is the first
	// day the source publishes. Start the day before it, and keep out anything
	// the source dates earlier (LB's archive has a row the day before its
	// start).
	var floor time.Time
	if !after.IsZero() && after.Equal(p.CoverageStart) {
		floor = after
		after = after.AddDate(0, 0, -1)
	}

	a, err := in.adapter(p.Key)
	if err != nil {
		return err
	}
	fetched := false
	yield := func(records []adapter.Rate) error {
		if !floor.IsZero() {
			records = slices.DeleteFunc(records, func(r adapter.Rate) bool { return r.Date.Before(floor) })
		}
		fetched = true
		return in.store(ctx, p, a, records, today, log)
	}
	if e, ok := a.(adapter.EachFetcher); ok {
		err = e.FetchEach(ctx, after, yield)
	} else {
		err = adapter.FetchEach(ctx, a, after, today, yield)
	}
	if err != nil {
		return err
	}
	if !fetched {
		log.Info("fetched no records")
	}
	return nil
}

func (in *Ingester) store(ctx context.Context, p Provider, a adapter.Adapter, records []adapter.Rate, today time.Time,
	log *slog.Logger,
) error {
	records = rates.Reject(records, a.LeadDays(), today)
	for i := range records {
		records[i].Rate = rates.Normalize(records[i].Rate)
	}
	if a.Revises() {
		if err := in.warnRevisions(ctx, p, records, log); err != nil {
			return err
		}
	}

	inserted, err := in.insert(ctx, p, records)
	if err != nil {
		return err
	}

	log.Info("inserted rates", "count", inserted)
	if inserted == 0 {
		return nil
	}
	// Purge stays last: purging before the blend refresh commits would let the
	// edge re-cache stale blends.
	if in.Cache != nil {
		if err := in.Cache.PurgeDebounced(ctx); err != nil {
			return fmt.Errorf("purge cache: %w", err)
		}
	}
	return in.optimize(ctx)
}

// insert stores records and, when any are new, refreshes what they feed, in
// one write transaction under the write lock. It returns how many rows were
// new.
func (in *Ingester) insert(ctx context.Context, p Provider, records []adapter.Rate) (int64, error) {
	in.writes.Lock()
	defer in.writes.Unlock()
	var inserted int64
	err := db.Immediate(ctx, in.DB, func(q db.Querier) error {
		for _, r := range records {
			c := rates.ComponentsOf(r)
			res, err := q.ExecContext(ctx, `INSERT INTO rates (provider, date, base, quote, mid, bid, ask)
				VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT (provider, date, base, quote) DO NOTHING`,
				p.Key, db.FormatDate(r.Date), r.Base, r.Quote, c.Mid, c.Bid, c.Ask)
			if err != nil {
				return fmt.Errorf("insert rates: %w", err)
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			inserted += n
		}
		if inserted == 0 {
			return nil
		}
		return in.refresh(ctx, q, p, records)
	})
	return inserted, err
}

// optimize runs PRAGMA optimize under the write lock, since the ANALYZE it may
// run writes the statistics tables.
func (in *Ingester) optimize(ctx context.Context) error {
	in.writes.Lock()
	defer in.writes.Unlock()
	_, err := in.DB.ExecContext(ctx, "PRAGMA optimize")
	return err
}

// refresh rebuilds what the inserted records feed: provider rollups and, for
// blending providers, their blended buckets, currency summaries, and the stored
// daily blend.
func (in *Ingester) refresh(ctx context.Context, q db.Querier, p Provider, records []adapter.Rate) error {
	var dates []time.Time
	var codes []string
	for _, r := range records {
		if !slices.ContainsFunc(dates, r.Date.Equal) {
			dates = append(dates, r.Date)
		}
		for _, c := range []string{r.Base, r.Quote} {
			if !slices.Contains(codes, c) {
				codes = append(codes, c)
			}
		}
	}

	buckets, err := rates.RefreshRollups(ctx, q, p.Key, dates)
	if err != nil {
		return err
	}
	if p.Blends() {
		if err := in.blend().RefreshRollupsTx(ctx, q, buckets); err != nil {
			return fmt.Errorf("refresh blended rollups: %w", err)
		}
	}
	if err := rates.RefreshSummaries(ctx, q, codes, p.Key); err != nil {
		return err
	}
	if !p.Blends() {
		return nil
	}
	// A late arrival at date d joins the carry-forward contributor set of
	// anchors through d + LookbackDays, so those stored blends change too.
	// Inside the transaction: the write lock serialises concurrent backfills'
	// refreshes, and a failed refresh rolls back the insert so the next fetch
	// re-ingests and retries.
	first, last := slices.MinFunc(dates, time.Time.Compare), slices.MaxFunc(dates, time.Time.Compare)
	if err := in.blend().RefreshTx(ctx, q, first, last.AddDate(0, 0, rates.LookbackDays)); err != nil {
		return fmt.Errorf("refresh blend: %w", err)
	}
	return nil
}

// warnRevisions reports fetched rows whose stored value differs. Insert-only
// backfill never rewrites a stored row, so a source that revises a published
// value in place leaves us holding the old one; the fix is the documented
// delete-and-refetch.
func (in *Ingester) warnRevisions(ctx context.Context, p Provider, records []adapter.Rate, log *slog.Logger) error {
	if len(records) == 0 {
		return nil
	}
	var dates []string
	for _, r := range records {
		if d := db.FormatDate(r.Date); !slices.Contains(dates, d) {
			dates = append(dates, d)
		}
	}
	rows, err := in.DB.QueryContext(ctx, "SELECT date, base, quote, rate FROM rates WHERE provider = ? AND date IN "+
		db.LitList(dates), p.Key)
	if err != nil {
		return err
	}
	stored := map[string]float64{}
	for rows.Next() {
		var date db.NullDate
		var base, quote string
		var rate sql.NullFloat64
		if err := rows.Scan(&date, &base, &quote, &rate); err != nil {
			rows.Close()
			return err
		}
		if rate.Valid {
			stored[db.FormatDate(date.Time)+" "+base+"/"+quote] = rate.Float64
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	var detail []string
	drifted := 0
	for _, r := range records {
		pair := db.FormatDate(r.Date) + " " + r.Base + "/" + r.Quote
		value, ok := stored[pair]
		if !ok || value == r.Rate {
			continue
		}
		drifted++
		if len(detail) < 5 {
			detail = append(detail, pair+" stored "+formatFloat(value)+" fetched "+formatFloat(r.Rate))
		}
	}
	if drifted > 0 {
		log.Warn("stored rates differ from source", "count", drifted, "detail", strings.Join(detail, ", "))
	}
	return nil
}

func formatFloat(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
