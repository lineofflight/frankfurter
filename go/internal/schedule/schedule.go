package schedule

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/provider"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// Blend is the materialized blend, owned by the blending step. Each method runs
// its own transactions.
type Blend interface {
	// Refresh recomputes stored daily blends for [from, to]
	// (BlendedRate.refresh).
	Refresh(ctx context.Context, from, to time.Time) error
	// Ready reports whether the daily blend covers full history
	// (BlendedRate.ready?).
	Ready(ctx context.Context) (bool, error)
	// Rebuild rebuilds the daily blend (BlendedRate.rebuild).
	Rebuild(ctx context.Context) error
	// Populate fills the missing buckets of the blended rollup at precision
	// rates.Week or rates.Month and returns how many rows it wrote
	// (BlendedWeeklyRate.populate, BlendedMonthlyRate.populate).
	Populate(ctx context.Context, p rates.Precision) (int, error)
}

// Cache is the CDN cache, owned by the cache step.
type Cache interface {
	PurgeDebounced(ctx context.Context) error
	// PurgePending flushes a purge the debounce deferred once its window has
	// expired (Cache.purge_pending).
	PurgePending(ctx context.Context) error
}

// Deps is what Setup wires together.
type Deps struct {
	Providers []provider.Provider
	Backfill  func(ctx context.Context, p provider.Provider)

	// Blend and Cache jobs are left out when nil.
	Blend Blend
	Cache Cache

	// Today defaults to rates.Today; Shuffle to a random permutation.
	Today   func() time.Time
	Shuffle func([]provider.Provider)
}

// StartupStagger spaces the startup backfills to avoid a thundering herd.
const StartupStagger = 2 * time.Second

// Setup registers bin/schedule's jobs on r.
func Setup(r Registrar, d Deps) error {
	today := d.Today
	if today == nil {
		today = rates.Today
	}

	if d.Cache != nil {
		// Trailing edge of the purge debounce: flush a purge the backfills
		// coalesced once its window expires.
		r.Every("purge pending", time.Minute, Options{}, func(ctx context.Context, _ *Job) error {
			return d.Cache.PurgePending(ctx)
		})
	}

	if d.Blend != nil {
		// Re-blend the trailing window at midnight: a forward-dated observation
		// blended earlier froze that day's decay weights (the weighted average
		// caps its reference date at today), so each new date needs a
		// recompute. Ingest accepts value dates up to two days ahead, hence
		// through tomorrow (#570). Purge after, so edges do not revalidate
		// stale blends into a fresh day.
		if err := r.Cron("midnight blend", "0 0 * * *", Options{}, func(ctx context.Context, _ *Job) error {
			t := today()
			if err := d.Blend.Refresh(ctx, t.AddDate(0, 0, -1), t.AddDate(0, 0, 1)); err != nil {
				return err
			}
			return purge(ctx, d.Cache)
		}); err != nil {
			return err
		}

		// Materialise the blend on installs that have never built it. Until it
		// completes, plain ranges fall back to capped live compute; the purge
		// afterwards drops responses cached off that fallback. Retries after
		// transient failures and unschedules once populated, including
		// legitimately empty buckets that cannot produce a USD blend.
		r.Every("populate blends", 5*time.Minute, Options{FirstIn: 30 * time.Second, NoOverlap: true},
			func(ctx context.Context, job *Job) error { return populate(ctx, d.Blend, d.Cache, job) })
	}

	for i, p := range shuffled(d) {
		r.In("backfill "+p.Key, time.Duration(i)*StartupStagger, func(ctx context.Context, _ *Job) error {
			d.Backfill(ctx, p)
			return nil
		})
	}

	for _, p := range d.Providers {
		if p.PublishSchedule == "" {
			continue
		}
		if err := r.Cron("backfill "+p.Key, p.PublishSchedule, Options{NoOverlap: true},
			func(ctx context.Context, _ *Job) error {
				d.Backfill(ctx, p)
				return nil
			}); err != nil {
			return err
		}
	}
	return nil
}

func populate(ctx context.Context, b Blend, c Cache, job *Job) error {
	ready, err := b.Ready(ctx)
	if err != nil {
		return err
	}
	if !ready {
		err := b.Rebuild(ctx)
		// Purge even on failure: a late failure can leave a committed
		// materialization ready, and the retry would then skip this rebuild and
		// its purge.
		if perr := purge(ctx, c); err == nil {
			err = perr
		}
		if err != nil {
			return err
		}
	}
	for _, p := range []rates.Precision{rates.Week, rates.Month} {
		n, err := b.Populate(ctx, p)
		if err != nil {
			return err
		}
		if n > 0 {
			if err := purge(ctx, c); err != nil {
				return err
			}
		}
	}
	job.Unschedule()
	return nil
}

func purge(ctx context.Context, c Cache) error {
	if c == nil {
		return nil
	}
	return c.PurgeDebounced(ctx)
}

func shuffled(d Deps) []provider.Provider {
	out := append([]provider.Provider(nil), d.Providers...)
	if d.Shuffle != nil {
		d.Shuffle(out)
	} else {
		rand.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	}
	return out
}

// DryRun prints what Setup would schedule for providers, as bin/schedule
// --dry-run does.
func DryRun(w io.Writer, providers []provider.Provider) error {
	d := Deps{Providers: providers}
	for _, p := range shuffled(d) {
		if _, err := fmt.Fprintf(w, "startup: backfill[%s]\n", strings.ToLower(p.Key)); err != nil {
			return err
		}
	}
	for _, p := range providers {
		if p.PublishSchedule == "" {
			continue
		}
		if _, err := fmt.Fprintf(w, "cron: %s backfill[%s]\n", p.PublishSchedule, strings.ToLower(p.Key)); err != nil {
			return err
		}
	}
	return nil
}
