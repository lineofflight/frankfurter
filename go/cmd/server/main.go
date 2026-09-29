// Command server runs the HTTP API (config.ru under config/puma.rb). It listens on PORT (default 8080) and shuts down
// gracefully on SIGINT or SIGTERM.
//
// Puma's worker processes and threads have no counterpart: one Go process serves requests concurrently, and
// MAX_THREADS still sizes the database pool (internal/db).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/api"
	"github.com/lineofflight/frankfurter/go/internal/applog"
	"github.com/lineofflight/frankfurter/go/internal/db"
)

func main() {
	applog.Setup()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	port, err := envInt("PORT", 8080)
	if err != nil {
		return err
	}
	path, err := db.DefaultPath()
	if err != nil {
		return err
	}
	conn, err := db.Open(path)
	if err != nil {
		return err
	}
	defer conn.Close()

	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(port),
		Handler:           (&api.Server{DB: conn}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", srv.Addr)
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
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
