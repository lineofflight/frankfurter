package main

import (
	"bytes"
	"context"
	"database/sql"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/cache"
	"github.com/lineofflight/frankfurter/go/internal/db"
	"github.com/lineofflight/frankfurter/go/internal/fixtures"
	"github.com/lineofflight/frankfurter/go/internal/migrate"
	"github.com/lineofflight/frankfurter/go/internal/provider"
	"github.com/lineofflight/frankfurter/go/internal/seeds"
)

// scratchDB points DATABASE_URL at a new, empty database file and returns its path.
func scratchDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "frankfurter.sqlite3")
	t.Setenv("DATABASE_URL", "sqlite://"+path)
	return path
}

// frankfurter runs the command line and returns its exit status and output.
func frankfurter(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	code := run(context.Background(), args, &out, &errw)
	return code, out.String(), errw.String()
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	code, out, errw := frankfurter(t, args...)
	if code != 0 {
		t.Fatalf("frankfurter %s: exit %d: %s", strings.Join(args, " "), code, errw)
	}
	return out
}

func openPath(t *testing.T, path string) *sql.DB {
	t.Helper()
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func schemaVersion(t *testing.T, path string) int {
	t.Helper()
	v, err := migrate.Current(context.Background(), openPath(t, path))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// purgeRecorder swaps in a configured cache whose purges hit a local server, and counts them.
func purgeRecorder(t *testing.T) (*atomic.Int32, func() *cache.Cache) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/zones/zone/purge_cache") {
			hits.Add(1)
		}
	}))
	t.Cleanup(srv.Close)
	build := func() *cache.Cache {
		c := cache.New("zone", "token")
		c.Endpoint = srv.URL
		c.Window = time.Hour
		return c
	}
	previous := newCache
	newCache = build
	t.Cleanup(func() { newCache = previous })
	return &hits, build
}

func TestHelpAndUnknownCommands(t *testing.T) {
	if code, out, _ := frankfurter(t, "help"); code != 0 || !strings.Contains(out, "rollups-rebuild [provider]") {
		t.Fatalf("help: exit %d\n%s", code, out)
	}
	if code, _, _ := frankfurter(t); code != 2 {
		t.Fatalf("no command: exit %d", code)
	}
	if code, _, errw := frankfurter(t, "db:rollback"); code != 2 || !strings.Contains(errw, "unknown command") {
		t.Fatalf("unknown command: exit %d: %s", code, errw)
	}
	if code, _, _ := frankfurter(t, "seed", "extra"); code != 1 {
		t.Fatalf("stray argument: exit %d", code)
	}
}

// db:setup migrates a new database to the latest version and seeds every provider; a second run (every container
// start) is a no-op migration and a reseed.
func TestSetupMigratesAndSeeds(t *testing.T) {
	path := scratchDB(t)
	mustRun(t, "setup")
	mustRun(t, "db:setup")
	if v := schemaVersion(t, path); v != migrate.Latest() {
		t.Fatalf("version %d", v)
	}
	want, err := seeds.Providers()
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := openPath(t, path).QueryRow("SELECT count(*) FROM providers").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(want) {
		t.Fatalf("%d providers, want %d", n, len(want))
	}
}

// db:migrate goes to the latest version, to VERSION, or to -version.
func TestMigrateHonoursVersion(t *testing.T) {
	path := scratchDB(t)
	mustRun(t, "migrate", "-version", "35")
	if v := schemaVersion(t, path); v != 35 {
		t.Fatalf("version %d, want 35", v)
	}
	t.Setenv("VERSION", "37")
	mustRun(t, "db:migrate")
	if v := schemaVersion(t, path); v != 37 {
		t.Fatalf("version %d, want 37", v)
	}
	t.Setenv("VERSION", "latest")
	if code, _, _ := frankfurter(t, "migrate"); code != 1 {
		t.Fatalf("non-numeric VERSION: exit %d", code)
	}
	mustRun(t, "migrate", "-version", "40")
	if v := schemaVersion(t, path); v != 40 {
		t.Fatalf("version %d, want 40", v)
	}
}

// The dry run reads the providers table named by DATABASE_URL and prints one startup line per provider and one cron
// line per scheduled provider.
func TestDryRunReadsTheDatabase(t *testing.T) {
	conn := fixtures.New(t)
	path := filepath.Join(t.TempDir(), "copy.sqlite3")
	if _, err := conn.Exec("VACUUM INTO ?", path); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATABASE_URL", "sqlite://"+path)

	out := mustRun(t, "schedule", "-dry-run")
	var providers, scheduled int
	if err := conn.QueryRow("SELECT count(*), count(publish_schedule) FROM providers").Scan(&providers, &scheduled); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	startup, crons := 0, 0
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "startup: backfill["):
			startup++
		case strings.HasPrefix(l, "cron: "):
			crons++
		}
	}
	if startup != providers || crons != scheduled || len(lines) != providers+scheduled {
		t.Fatalf("startup %d/%d, cron %d/%d, lines %d", startup, providers, crons, scheduled, len(lines))
	}
}

// The scheduler's backfills and purge jobs share one cache, so the debounce spans them all, and the real blend is
// wired in.
func TestScheduleWiresBlendAndOneCache(t *testing.T) {
	c := cache.New("", "")
	deps := scheduleDeps(nil, nil, c)
	if deps.Cache != c {
		t.Fatal("scheduler has no cache")
	}
	if _, ok := deps.Blend.(provider.Materialized); !ok || deps.Backfill == nil {
		t.Fatalf("blend %T, backfill set %v", deps.Blend, deps.Backfill != nil)
	}
}

// debouncing is a backfill that inserts twice within one debounce window: the first purge fires, the second is
// deferred.
type debouncing struct{ c *cache.Cache }

func (d debouncing) Backfill(ctx context.Context, _ provider.Provider) {
	d.c.PurgeDebounced(ctx)
	d.c.PurgeDebounced(ctx)
}

func (d debouncing) BackfillAfter(ctx context.Context, p provider.Provider, _ time.Time) {
	d.Backfill(ctx, p)
}

// The backfill task ends by flushing the purge its debounce deferred, since the process is about to exit; an unknown
// provider aborts before backfilling or purging.
func TestBackfillFlushesTheDeferredPurge(t *testing.T) {
	path := scratchDB(t)
	mustRun(t, "setup")
	conn := openPath(t, path)
	hits, build := purgeRecorder(t)

	c := build()
	if err := backfillWith(context.Background(), conn, debouncing{c}, c, "ecb", false); err != nil {
		t.Fatal(err)
	}
	if n := hits.Load(); n != 2 {
		t.Fatalf("%d purges, want the immediate one and the flushed one", n)
	}

	hits.Store(0)
	c = build()
	if err := backfillWith(context.Background(), conn, debouncing{c}, c, "nope", false); err == nil ||
		!strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("unknown provider: %v", err)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("%d purges after an unknown provider", n)
	}
}

// The maintenance tasks purge the CDN as their rake counterparts do: after blend and rollup rebuilds, after
// purge_invalid only when it deleted something, and on cache:purge.
func TestTasksPurgeTheCache(t *testing.T) {
	scratchDB(t)
	mustRun(t, "setup")
	hits, _ := purgeRecorder(t)

	for _, tc := range []struct {
		args  []string
		code  int
		purge int32
	}{
		{[]string{"cache:purge"}, 0, 1},
		{[]string{"blend-rebuild"}, 0, 1},
		{[]string{"rollups-rebuild"}, 0, 1},
		{[]string{"rollups:rebuild", "ecb"}, 0, 1},
		{[]string{"rollups-rebuild", "nope"}, 1, 0},
		{[]string{"purge-invalid"}, 0, 0},
		{[]string{"consensus"}, 0, 0},
		{[]string{"consensus", "2024"}, 0, 0},
		{[]string{"consensus:recent"}, 0, 0},
	} {
		hits.Store(0)
		code, _, errw := frankfurter(t, tc.args...)
		if code != tc.code || hits.Load() != tc.purge {
			t.Errorf("%v: exit %d, %d purges; want exit %d, %d purges (%s)", tc.args, code, hits.Load(), tc.code,
				tc.purge, errw)
		}
	}
}

func TestHealthcheck(t *testing.T) {
	for _, tc := range []struct {
		status, code int
	}{{http.StatusOK, 0}, {http.StatusInternalServerError, 1}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
		}))
		u, _ := url.Parse(srv.URL)
		t.Setenv("PORT", u.Port())
		if code, _, errw := frankfurter(t, "healthcheck"); code != tc.code {
			t.Errorf("status %d: exit %d (%s)", tc.status, code, errw)
		}
		srv.Close()
	}
}

// db:purge_invalid purges the CDN once it has deleted something: a rate dated past ECB's future-date horizon.
func TestPurgeInvalidPurgesAfterDeleting(t *testing.T) {
	path := scratchDB(t)
	mustRun(t, "setup")
	hits, _ := purgeRecorder(t)
	conn := openPath(t, path)
	future := db.FormatDate(today().AddDate(0, 0, 30))
	if _, err := conn.Exec("INSERT INTO rates (provider, date, base, quote, mid) VALUES ('ECB', ?, 'EUR', 'USD', 1.1)",
		future); err != nil {
		t.Fatal(err)
	}

	mustRun(t, "db:purge_invalid")
	if n := hits.Load(); n != 1 {
		t.Fatalf("%d purges, want 1", n)
	}
	var n int
	if err := conn.QueryRow("SELECT count(*) FROM rates").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d rates left", n)
	}
}

// serve answers the API root, which the container healthcheck polls, and shuts down cleanly when interrupted.
func TestServeAnswersAndShutsDown(t *testing.T) {
	path := scratchDB(t)
	mustRun(t, "setup")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serveOn(ctx, openPath(t, path), ln) }()

	res, err := http.Get("http://" + ln.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /: status %d", res.StatusCode)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}
