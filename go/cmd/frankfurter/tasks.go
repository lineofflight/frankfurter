package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/blend"
	"github.com/lineofflight/frankfurter/go/internal/cache"
	"github.com/lineofflight/frankfurter/go/internal/migrate"
	"github.com/lineofflight/frankfurter/go/internal/provider"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// runMigrate is db:migrate: to the latest version, or to -version (VERSION in the environment, as rake takes it).
func runMigrate(ctx context.Context, args []string, _ io.Writer) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	target := fs.Int("version", -1, "target version (default: latest, or VERSION)")
	if _, err := flags(fs, args, 0); err != nil {
		return err
	}
	if *target < 0 {
		*target = migrate.Latest()
		if v, ok := os.LookupEnv("VERSION"); ok {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return fmt.Errorf("VERSION: %w", err)
			}
			*target = n
		}
	}
	return withDB(func(conn *sql.DB) error { return migrate.To(ctx, conn, *target) })
}

// seed is db:seed.
func seed(ctx context.Context, args []string, _ io.Writer) error {
	if _, err := flags(flag.NewFlagSet("seed", flag.ContinueOnError), args, 0); err != nil {
		return err
	}
	return withDB(func(conn *sql.DB) error { return rates.SeedProviders(ctx, conn) })
}

// runSetup is db:setup.
func runSetup(ctx context.Context, args []string, _ io.Writer) error {
	if _, err := flags(flag.NewFlagSet("setup", flag.ContinueOnError), args, 0); err != nil {
		return err
	}
	return withDB(func(conn *sql.DB) error { return setupDB(ctx, conn) })
}

// backfill is the backfill[provider] task.
func backfill(ctx context.Context, args []string, _ io.Writer) error {
	fs := flag.NewFlagSet("backfill", flag.ContinueOnError)
	full := fs.Bool("full", os.Getenv("FULL") == "1", "start at each provider's coverage_start (FULL=1)")
	rest, err := flags(fs, args, 1)
	if err != nil {
		return err
	}
	name := ""
	if len(rest) == 1 {
		name = rest[0]
	}
	return withDB(func(conn *sql.DB) error {
		c := newCache()
		return backfillWith(ctx, conn, &provider.Ingester{DB: conn, Cache: c}, c, name, *full)
	})
}

// backfillWith runs the backfill task on b and then, the wave being over and the process about to exit, flushes any
// purge the debounce deferred (Cache.purge_pending(ignore_window: true)). An unknown provider aborts before either.
func backfillWith(ctx context.Context, conn *sql.DB, b provider.Backfiller, c *cache.Cache, name string, full bool) error {
	providers, err := provider.All(ctx, conn)
	if err != nil {
		return err
	}
	if err := provider.BackfillTask(ctx, b, providers, name, full, conn.Stats().MaxOpenConnections); err != nil {
		return err
	}
	return c.FlushPending(ctx)
}

// blendRebuild is blend:rebuild. Rebuilds change served values, so cached responses must not outlive them; a failed
// rebuild skips the purge, as the task raises before it.
func blendRebuild(ctx context.Context, args []string, _ io.Writer) error {
	if _, err := flags(flag.NewFlagSet("blend-rebuild", flag.ContinueOnError), args, 0); err != nil {
		return err
	}
	return withDB(func(conn *sql.DB) error {
		if err := blend.RebuildAll(ctx, conn, today()); err != nil {
			return err
		}
		return newCache().Purge(ctx)
	})
}

// rollupsRebuild is rollups:rebuild[provider]. The purge runs even when the refill fails, since the source changes
// have committed by then.
func rollupsRebuild(ctx context.Context, args []string, _ io.Writer) error {
	rest, err := flags(flag.NewFlagSet("rollups-rebuild", flag.ContinueOnError), args, 1)
	if err != nil {
		return err
	}
	return withDB(func(conn *sql.DB) error {
		key := ""
		if len(rest) == 1 {
			if key, err = providerKey(ctx, conn, rest[0]); err != nil {
				return err
			}
		}
		err := blend.RebuildProviderRollups(ctx, conn, key, today())
		return errors.Join(err, newCache().Purge(ctx))
	})
}

// providerKey matches name to a provider key case-insensitively, as the tasks' Provider.detect with casecmp does.
func providerKey(ctx context.Context, conn *sql.DB, name string) (string, error) {
	providers, err := provider.All(ctx, conn)
	if err != nil {
		return "", err
	}
	for _, p := range providers {
		if strings.EqualFold(p.Key, name) {
			return p.Key, nil
		}
	}
	return "", fmt.Errorf("unknown provider: %s", name)
}

// consensus is consensus[year]: without a year, all stored history.
func consensus(ctx context.Context, args []string, _ io.Writer) error {
	rest, err := flags(flag.NewFlagSet("consensus", flag.ContinueOnError), args, 1)
	if err != nil {
		return err
	}
	return withDB(func(conn *sql.DB) error {
		if len(rest) == 0 {
			_, err := blend.ScanConsensus(ctx, conn, time.Time{}, time.Time{}, today())
			return err
		}
		year, err := strconv.Atoi(strings.TrimSpace(rest[0]))
		if err != nil {
			return fmt.Errorf("year: %w", err)
		}
		_, err = blend.ScanYearConsensus(ctx, conn, year, today())
		return err
	})
}

// consensusRecent is consensus:recent.
func consensusRecent(ctx context.Context, args []string, _ io.Writer) error {
	if _, err := flags(flag.NewFlagSet("consensus-recent", flag.ContinueOnError), args, 0); err != nil {
		return err
	}
	return withDB(func(conn *sql.DB) error {
		_, err := blend.ScanRecentConsensus(ctx, conn, today())
		return err
	})
}

// purgeInvalid is db:purge_invalid. When anything was deleted the CDN is purged even if a rebuild failed: the
// deletion has committed and must not leave old responses cached.
func purgeInvalid(ctx context.Context, args []string, _ io.Writer) error {
	if _, err := flags(flag.NewFlagSet("purge-invalid", flag.ContinueOnError), args, 0); err != nil {
		return err
	}
	return withDB(func(conn *sql.DB) error {
		leads, err := rates.ProviderLeads(ctx, conn)
		if err != nil {
			return err
		}
		totals, err := blend.PurgeInvalid(ctx, conn, today(), leads)
		if totals.Total() == 0 {
			return err
		}
		return errors.Join(err, newCache().Purge(ctx))
	})
}

// purgeCache is cache:purge.
func purgeCache(ctx context.Context, args []string, _ io.Writer) error {
	if _, err := flags(flag.NewFlagSet("purge-cache", flag.ContinueOnError), args, 0); err != nil {
		return err
	}
	return newCache().Purge(ctx)
}
