// Command schedule is bin/schedule: it backfills every provider at startup, staggered, then again on each provider's
// publish_schedule, until interrupted. --dry-run prints the plan instead.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/lineofflight/frankfurter/go/internal/adapters/all"
	"github.com/lineofflight/frankfurter/go/internal/applog"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/provider"
	"github.com/lineofflight/frankfurter/go/internal/schedule"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "print the schedule and exit")
	flag.Parse()
	applog.Setup()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Stdout, *dryRun); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, stdout io.Writer, dryRun bool) error {
	path, err := db.DefaultPath()
	if err != nil {
		return err
	}
	conn, err := db.Open(path)
	if err != nil {
		return err
	}
	defer conn.Close()
	providers, err := provider.All(ctx, conn)
	if err != nil {
		return err
	}
	if dryRun {
		return schedule.DryRun(stdout, providers)
	}

	in := &provider.Ingester{DB: conn}
	s := schedule.New(conn.Stats().MaxOpenConnections, slog.Default())
	// Cache stays nil until the cache step provides it; wire it here and into the Ingester.
	deps := schedule.Deps{Providers: providers, Backfill: in.Backfill, Blend: provider.Materialized{DB: conn}}
	if err := schedule.Setup(s, deps); err != nil {
		return err
	}
	s.Run(ctx)
	return nil
}
