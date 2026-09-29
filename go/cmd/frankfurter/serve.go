package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/api"
	"github.com/lineofflight/frankfurter/go/internal/migrate"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// serve is config.ru under config/puma.rb: the API on PORT (default 8080) until interrupted, then a graceful
// shutdown. Puma's worker processes and threads have no counterpart; one process serves concurrently and MAX_THREADS
// still sizes the database pool.
func serve(ctx context.Context, args []string, _ io.Writer) error {
	if _, err := flags(flag.NewFlagSet("serve", flag.ContinueOnError), args, 0); err != nil {
		return err
	}
	return withDB(func(conn *sql.DB) error { return listen(ctx, conn) })
}

func listen(ctx context.Context, conn *sql.DB) error {
	port, err := envInt("PORT", 8080)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return err
	}
	return serveOn(ctx, conn, ln)
}

// serveOn serves the API on ln until ctx ends, then shuts down gracefully.
func serveOn(ctx context.Context, conn *sql.DB, ln net.Listener) error {
	srv := &http.Server{
		Handler:           (&api.Server{DB: conn}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", ln.Addr().String())
		errc <- srv.Serve(ln)
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// start is the container's command, `rake db:setup && foreman start`: set up the database, then run the web and
// scheduler processes of the Procfile side by side until interrupted. Each gets its own connection pool, as the two
// processes do in Ruby, so long backfill transactions never starve requests. When either stops, so does the other.
func start(ctx context.Context, args []string, stdout io.Writer) error {
	if _, err := flags(flag.NewFlagSet("start", flag.ContinueOnError), args, 0); err != nil {
		return err
	}
	if err := withDB(func(conn *sql.DB) error { return setupDB(ctx, conn) }); err != nil {
		return err
	}
	web, err := open()
	if err != nil {
		return err
	}
	defer web.Close()
	scheduler, err := open()
	if err != nil {
		return err
	}
	defer scheduler.Close()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, 2)
	go func() { errc <- listen(ctx, web) }()
	go func() { errc <- scheduleOn(ctx, scheduler, stdout, false) }()
	first := <-errc
	cancel()
	second := <-errc
	return errors.Join(first, second)
}

// setupDB is db:setup: migrate to the latest version, then reseed providers.
func setupDB(ctx context.Context, conn *sql.DB) error {
	if err := migrate.Up(ctx, conn); err != nil {
		return err
	}
	return rates.SeedProviders(ctx, conn)
}

// healthcheck replaces the Dockerfile's `curl -f --max-time 9 http://localhost:$PORT`, since the image has no curl.
func healthcheck(ctx context.Context, args []string, _ io.Writer) error {
	if _, err := flags(flag.NewFlagSet("healthcheck", flag.ContinueOnError), args, 0); err != nil {
		return err
	}
	port, err := envInt("PORT", 8080)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 9*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:"+strconv.Itoa(port), nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode >= 400 {
		return fmt.Errorf("status %d", res.StatusCode)
	}
	return nil
}

// envInt reads an integer the way Ruby's Integer() does, or returns fallback when the variable is unset.
func envInt(key string, fallback int) (int, error) {
	s, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 0, 0)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return int(n), nil
}
