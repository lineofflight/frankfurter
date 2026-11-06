// Package applog configures the process-wide slog logger, as lib/log.rb
// configures Ruby's: text to stdout at info level, errors only under
// APP_ENV=test. Library code logs through slog's package functions.
package applog

import (
	"io"
	"log/slog"
	"os"
)

// New returns the logger for the given APP_ENV, writing to w.
func New(w io.Writer, env string) *slog.Logger {
	level := slog.LevelInfo
	if env == "test" {
		level = slog.LevelError
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}

// Setup installs New(os.Stdout, $APP_ENV) as slog's default. Binaries call it
// first thing in main.
func Setup() {
	slog.SetDefault(New(os.Stdout, os.Getenv("APP_ENV")))
}
