// Command backfill is the backfill rake task: `backfill [provider]` backfills one provider, or every provider in
// parallel, incrementally by default. FULL=1 (or -full) starts each provider at its coverage_start.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/lineofflight/frankfurter/go/internal/adapters/all"
	"github.com/lineofflight/frankfurter/go/internal/applog"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/provider"
)

func main() {
	full := flag.Bool("full", os.Getenv("FULL") == "1", "start at each provider's coverage_start")
	flag.Parse()
	applog.Setup()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, flag.Arg(0), *full); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, name string, full bool) error {
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
	// Cache stays nil until the cache step provides it. Once it exists, flush the purge the
	// debounce deferred here (Cache.purge_pending(ignore_window: true)): the wave is over and the process exits.
	in := &provider.Ingester{DB: conn}
	return provider.BackfillTask(ctx, in, providers, name, full, conn.Stats().MaxOpenConnections)
}
