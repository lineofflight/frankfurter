package main

import (
	"context"
	"database/sql"
	"flag"
	"io"
	"log/slog"

	"github.com/lineofflight/frankfurter/go/internal/cache"
	"github.com/lineofflight/frankfurter/go/internal/provider"
	"github.com/lineofflight/frankfurter/go/internal/schedule"
)

// runSchedule is bin/schedule: backfill every provider at startup, staggered,
// then on each provider's publish_schedule, with the blend and cache-purge
// jobs, until interrupted. -dry-run prints the plan instead.
func runSchedule(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("schedule", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "print the schedule and exit")
	if _, err := flags(fs, args, 0); err != nil {
		return err
	}
	return withDB(func(conn *sql.DB) error { return scheduleOn(ctx, conn, stdout, *dryRun) })
}

func scheduleOn(ctx context.Context, conn *sql.DB, stdout io.Writer, dryRun bool) error {
	providers, err := provider.All(ctx, conn)
	if err != nil {
		return err
	}
	if dryRun {
		return schedule.DryRun(stdout, providers)
	}
	s := schedule.New(conn.Stats().MaxOpenConnections, slog.Default())
	if err := schedule.Setup(s, scheduleDeps(conn, providers, newCache())); err != nil {
		return err
	}
	s.Run(ctx)
	return nil
}

// scheduleDeps wires the real backfill, blend and cache into the scheduler. The
// backfills and the scheduler's own purge jobs share one Cache, so its debounce
// spans them all.
func scheduleDeps(conn *sql.DB, providers []provider.Provider, c *cache.Cache) schedule.Deps {
	in := &provider.Ingester{DB: conn, Cache: c}
	return schedule.Deps{
		Providers: providers,
		Backfill:  in.Backfill,
		Blend:     provider.Materialized{DB: conn},
		Cache:     c,
	}
}
