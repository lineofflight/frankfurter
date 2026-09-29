# Core step: api_v1

The HTTP app (lib/app.rb and its middleware), the frozen v1 API, static files and OpenAPI documents, the CDN cache
purge, and the server binary. `go test ./internal/api ./internal/cache` runs everything.

## Packages

### `internal/api` (lib/app.rb, lib/versions/v1.rb and v1/**, lib/noindex.rb, lib/no_store_on_error.rb, lib/request_timeout.rb, Rack::Cors setup, lib/public/**)

- `Server{DB, Today, Timeout, HeavySlots}` and `(*Server).Handler() http.Handler`: the app wrapped in its middleware,
  outermost first as app.rb lists it: v1 deprecation, request timeout, no-store on errors, noindex, CORS, then the
  `http.ServeMux`. `Today` defaults to `rates.Today`, `Timeout` to `DefaultTimeout` (REQUEST_TIMEOUT_SECONDS, default
  90, parsed like `Integer()`). `HeavySlots` is unused by v1; it is there for v2 (see below).
- `RequestDeadline(r) time.Time`: when the request's budget runs out (set by the timeout middleware on the context).
  `TimeoutError{Timeout}` is `RequestTimeout::Error` (message "request exceeded 90s timeout").
- Files: `api.go` (Server, root, not found, `writeJSON`, version registration), `middleware.go`, `static.go`
  (embedded `public/`), `v1.go` (routes, deprecation, `etag`), `v1_query.go`, `v1_quote.go`, `v1_currencies.go`.
- `public/` is a byte copy of `lib/public` (go:embed cannot reach outside the module). After editing `lib/public`, run
  `go generate ./internal/api`; `TestPublicMatchesRepository` fails while the copies differ.

### `internal/cache` (lib/cache.rb)

- `Cache{ZoneID, APIToken, Endpoint, Client, Window}`; `New(zone, token)`, `FromEnv()` (CLOUDFLARE_ZONE_ID,
  CLOUDFLARE_API_TOKEN). Share one per process: the debounce state lives on it.
- `Purge(ctx)` (no-op unless both credentials are set; error on non-2xx), `PurgeDebounced(ctx)`,
  `PurgePending(ctx)` (`purge_pending`), `FlushPending(ctx)` (`purge_pending(ignore_window: true)`).
  `DefaultWindow` is CACHE_PURGE_DEBOUNCE_SECONDS (default 300).
- `*Cache` satisfies `provider.Cache` and `schedule.Cache` (checked in `iface_test.go`).

### `cmd/server` (config.ru, config/puma.rb)

Opens `db.DefaultPath()`, serves `(&api.Server{DB: conn}).Handler()` on PORT (default 8080), shuts down gracefully on
SIGINT/SIGTERM. Puma's workers and threads have no counterpart; MAX_THREADS still sizes the pool in `db.Open`.

## For the api_v2 step

- **Routes.** Put v2 in its own files and register from `init`: `func init() { registerVersion((*Server).routesV2) }`
  with `func (s *Server) routesV2(mux *http.ServeMux)`. Register only `/v2` paths. `/v2/openapi.json` is already served
  statically (don't register it). Unmatched paths already fall to the app's JSON 404 (`notFound(w, contentTypeJSON)`,
  body `{"status":404,"message":"not found"}`, `application/json`).
- **Middleware already covers v2**: noindex exempts `/v2` and `/v2/openapi.json`; no-store on any status >= 400; CORS
  and `Vary: Origin` everywhere. Error responses (>= 400) pass the timeout middleware untouched, so a 503 v2 generates
  after the deadline survives. For v2's own deadline checks use `RequestDeadline(r)` (Ruby's RateQuery starts its
  clock at construction; the request start is the same moment for practical purposes).
- **Helpers** in this package: `writeJSON(w, status, contentType, v)` (no HTML escaping, no trailing newline, as Oj),
  `message{Status, Message}`, `etag(w, r, value) bool` (Roda's `r.etag`: strong tag, 304 to GET/HEAD, 412 otherwise,
  `"*"` matches), `cacheOneDay`, `contentTypeJSON`, `(*Server).today()`.
- **Heavy slots.** `Server.HeavySlots` is the stand-in for `RateQuery.heavy_slots`. Default it inside the v2 files
  (e.g. a package-level `heavyslots.New(heavyslots.DefaultMax)` used when the field is nil); app_spec's heavy-cap case
  sets the field to an exhausted `heavyslots.New(1)`.
- **app_spec cases left for v2** (they need the v2 routes; add them in a v2 test file): "serves v2 root", "delivers a
  v2 deadline 503 through the middleware stack", "does not cache a 503 from the heavy compute cap", the three v2 rows of
  "error responses are not cached" (`/v2/rates?date=not-a-date` 422, `/v2/currency/xyz` 404, `/v2/currencies.csv`
  406), and `/v2` in "leaves ... indexable" (`TestLeavesEntryPointsIndexable` lists the rest). The v2 rows of "sets
  X-Robots-Tag" and "does not deprecate other routes" already run: they hold whatever the status.
- Also still open from earlier steps: spec/reciprocal_consistency_spec.rb, lib/rate_coverage.rb, blend parity and the
  query-level rollup cases (see core-domain.md and core-blend.md).
- **OpenAPI.** `openapi_test.go` has `loadSpec(t, "v2/openapi.json")` and `validateResponse(t, doc, specPath, req,
  res)`, which validates against a named operation (no router: v1's templates put two variables in one segment). v1's
  document fails kin-openapi's example validation, so `loadSpec` disables it.

## API golden harness

`go/scripts/api_golden.rb` seeds the spec fixture with `Date.today` pinned, adds edge rows (an ECB series seen on one
day only, an ECB holiday, an unknown and an expired code on the latest day; rollups and currencies rebuilt after), runs
every request in `internal/api/testdata/corpus.txt` through `App.freeze.app` with `Rack::MockRequest` env, and writes
`internal/api/testdata/golden/api.json.gz`: every table of the database (rates without the generated `rate` column)
and, per request, status, response headers and body (parsed JSON; text; or SHA-256 for binary).

`TestGoldenAPI` loads those tables into a fresh `dbtest` database, builds the handler with `Today` pinned to the
recorded date, replays each request, and compares:

- status;
- the headers in `goldenHeaders` (cache-control, content-type, deprecation, link, x-robots-tag, vary, etag,
  retry-after, allow, the CORS headers) by value, and by absence when Ruby sent none; content-type is skipped on empty
  bodies;
- the body: JSON semantically (object keys in any order, arrays in order, numbers within 1e-9 relative; on status >=
  400 the `message` text may differ but must be a non-empty string), text exactly, binary by hash. HEAD bodies are
  skipped (Roda leaves stripping them to Puma; Go's server strips them itself).

`TestV1ResponsesMatchOpenAPI` also validates the Go responses to the corpus's v1 GETs against `v1/openapi.json`.

Corpus lines are `METHOD PATH [| Header: value]...`; date tokens `{today}`, `{tomorrow}`, `{latest}`, `{sunday}`,
`{bday:N}`, `{ago:N}` expand in Ruby, and the golden file records the expanded path, so Go never recomputes dates. To
extend: append lines (v2 requests go in a new section), add edge rows to the script if needed (they change the data
under every request, so rerun and review the whole diff), then regenerate:

```sh
cd /Users/hakanensari/code/frankfurter/.claude/worktrees/go-port
DATABASE_URL=sqlite://$SCRATCH/api.sqlite3 APP_ENV=test \
  mise exec -- bundle exec ruby -Ilib -r./boot go/scripts/api_golden.rb 2026-09-29 \
  go/internal/api/testdata/corpus.txt go/internal/api/testdata/golden/api.json.gz
```

The database file must be a throwaway (the script migrates and reseeds it). The blended tables are empty after
`Fixtures.seed!`; if v2 needs materialized blends, have the script run `blend:rebuild` (and the rollup blends) after
the edge rows, as `blend_golden.rb` does. If v2 bodies need a looser comparison (CSV number formatting, say), extend
`checkGolden` per content type rather than loosening the JSON rules.

The v1 corpus (87 requests) covers the index, 404s, static files with HEAD/POST, every v1 route with amounts
(`to_f` quirks), bases, symbols (empty, unknown, expired, EUR), circular pairs, bad escapes, conditional requests,
CORS preflights (allowed and denied methods), carry-forward across the holiday and the lone series, open, closed,
reversed, future and empty intervals, and invalid dates. All match Ruby.

## Specs ported

app_spec (all but the v2 cases above), cache_spec, request_timeout_spec, edge_cases_spec, versions/v1_spec,
versions/v1/query_spec, versions/v1/quote/{base,end_of_day,interval}_spec, versions/v1/currency_names_spec,
versions/v1/roundable_spec (against `rates.Round`).

- v1_spec mounts `Versions::V1` alone at `/`; Go tests go through the full app at `/v1/...`.
- base_spec's "does not know how to format result / generate a cache key" (NotImplementedError) become a check that
  the base quote type has neither method; only `v1EndOfDay` and `v1Interval` do.
- request_timeout_spec's `seconds: 0` becomes a deadline already in the past (Ruby relies on the clock moving before
  the body is read). "delegates close to the wrapped body" becomes "aborts mid-stream": in Go the handler owns its
  resources, and a write after the deadline once the response has started panics with `http.ErrAbortHandler`.
- cache_spec's `expire_window!` advances an injected clock by the window; `Cache.stub(:purge, ...)` is the unexported
  `purge` seam; WebMock stubs are an `httptest` server behind `Endpoint`.

## Deviations

- The v1 quote SQL orders by date, base, quote explicitly (Ruby leaves order to SQLite's plan, which is the same
  index order). The order matters: an EndOfDay snapshot reports the first series' date, so a stale lone series that
  sorts first would set the response date, exactly as in Ruby (the golden edge rows exercise it).
- A stored row with no resolvable rate (a single published side, NaN in Go) is skipped by v1; Ruby's
  `amount * nil` raises and V1's error handler answers 422. ECB always publishes a mid, so this never happens.
- V1's error handler turns every exception into 422. Go answers 422 for request errors (amount, currency pair, dates,
  %-encoding, a query `date=` on an interval route) and 500 for database failures.
- Only the query string feeds v1 parameters; Roda's indifferent params would also merge a form body.
- A success response whose body is first written after the deadline becomes a 500 JSON with `no-store` (Puma answers
  a raising body with a bare 500). JSON floats print shortest (`1` where Oj writes `1.0`); `Roundable` values over
  5000 are floats (Ruby returns Integers).
- `net/http`'s ServeMux cleans paths (`/v1//latest` redirects) and decodes them before matching, where Roda matches
  the raw PATH_INFO.
- Static files carry no Last-Modified (embedded files have no mtime); ranges and conditional GETs still work.
- `Cache` treats empty credentials as unconfigured (Ruby checks only for nil, so an empty env var would try to purge).

## For the integrator

Wire the cache (the provider step left the hooks nil): in `cmd/backfill`, `c := cache.FromEnv()`, set
`provider.Ingester{..., Cache: c}`, and call `c.FlushPending(ctx)` after `BackfillTask` (Ruby's
`Cache.purge_pending(ignore_window: true)`); in `cmd/schedule`, pass the same kind of instance as `schedule.Deps.Cache`
and to the ingester. The blend and rollup tasks (`blend.RebuildAll` and friends) leave `Cache.purge` to their callers:
call `c.Purge(ctx)` after them as core-blend.md describes.
