// Command frankfurter is the whole Frankfurter app in one binary: the API server and scheduler the Procfile runs, and
// the maintenance tasks the Rakefile defines.
//
//	frankfurter <command> [flags] [args]
//
// Run it from the directory holding db/, or set DATABASE_URL=sqlite://path, exactly as the Ruby app. `frankfurter
// help` lists the commands; the rake task names (db:migrate, blend:rebuild, ...) work as aliases.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	_ "github.com/lineofflight/frankfurter/go/internal/adapters/all"
	"github.com/lineofflight/frankfurter/go/internal/applog"
	"github.com/lineofflight/frankfurter/go/internal/cache"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/rates"
)

// command is one subcommand. run gets the arguments after the command name.
type command struct {
	name    string
	aliases []string // Procfile or rake names
	args    string
	summary string
	run     func(ctx context.Context, args []string, stdout io.Writer) error
}

var commands []command

func init() {
	commands = []command{
		{"serve", []string{"web"}, "", "serve the API on PORT", serve},
		{"schedule", []string{"scheduler"}, "[-dry-run]", "backfill on each provider's schedule and maintain blends", runSchedule},
		{"start", nil, "", "set up the database, then serve and schedule in one process (the container's command)", start},
		{"migrate", []string{"db:migrate"}, "[-version N]", "run database migrations, to the latest or VERSION", runMigrate},
		{"seed", []string{"db:seed"}, "", "reseed the providers table from db/seeds", seed},
		{"setup", []string{"db:setup"}, "", "migrate and seed", runSetup},
		{"backfill", nil, "[-full] [provider]", "backfill rates, incrementally unless -full or FULL=1", backfill},
		{"blend-rebuild", []string{"blend:rebuild"}, "", "rebuild the daily, weekly and monthly blends, then purge the CDN", blendRebuild},
		{"rollups-rebuild", []string{"rollups:rebuild"}, "[provider]", "rebuild weekly and monthly rollups (all or one provider), then purge the CDN", rollupsRebuild},
		{"consensus", nil, "[year]", "log consensus outliers across all history or one year", consensus},
		{"consensus-recent", []string{"consensus:recent"}, "", "log consensus outliers over the last 365 days", consensusRecent},
		{"purge-invalid", []string{"db:purge_invalid"}, "", "purge rates beyond each provider's future-date horizon", purgeInvalid},
		{"purge-cache", []string{"cache:purge"}, "", "purge the CDN cache", purgeCache},
		{"healthcheck", nil, "", "exit non-zero unless the local server answers (the container's HEALTHCHECK)", healthcheck},
	}
}

// Seams for tests.
var (
	newCache = cache.FromEnv
	today    = rates.Today
)

func main() {
	applog.Setup()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run dispatches args to a command and returns the exit status.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		usage(stdout)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	c, ok := lookup(args[0])
	if !ok {
		fmt.Fprintf(stderr, "frankfurter: unknown command %q\n\n", args[0])
		usage(stderr)
		return 2
	}
	if err := c.run(ctx, args[1:], stdout); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintf(stderr, "frankfurter %s: %v\n", c.name, err)
		return 1
	}
	return 0
}

func lookup(name string) (command, bool) {
	for _, c := range commands {
		if c.name == name {
			return c, true
		}
		for _, a := range c.aliases {
			if a == name {
				return c, true
			}
		}
	}
	return command{}, false
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: frankfurter <command> [flags] [args]")
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range commands {
		name := c.name
		if c.args != "" {
			name += " " + c.args
		}
		alias := ""
		if len(c.aliases) > 0 {
			alias = " (" + strings.Join(c.aliases, ", ") + ")"
		}
		fmt.Fprintf(tw, "  %s\t%s%s\n", name, c.summary, alias)
	}
	tw.Flush()
}

// flags parses a command's flags and returns the remaining arguments, rejecting more than maxArgs of them.
func flags(fs *flag.FlagSet, args []string, maxArgs int) ([]string, error) {
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > maxArgs {
		return nil, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args()[maxArgs:], " "))
	}
	return fs.Args(), nil
}

// open opens the database lib/db.rb would: DATABASE_URL, or db/frankfurter[_APP_ENV].sqlite3 under the working
// directory.
func open() (*sql.DB, error) {
	path, err := db.DefaultPath()
	if err != nil {
		return nil, err
	}
	return db.Open(path)
}

// withDB runs fn on a freshly opened database and closes it afterwards.
func withDB(fn func(conn *sql.DB) error) error {
	conn, err := open()
	if err != nil {
		return err
	}
	defer conn.Close()
	return fn(conn)
}

// shutdownGrace bounds how long serve waits for in-flight requests on shutdown.
const shutdownGrace = 30 * time.Second
