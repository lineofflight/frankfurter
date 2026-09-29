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
  `message{Status, Message}`, `etag(w, r, value) bool` (Roda's `r.etag`: strong tag; a matching If-None-Match gets
  304 to GET/HEAD/OPTIONS/TRACE and 412 otherwise, `"*"` matching except on POST; a non-matching If-Match gets 412),
  `cacheOneDay`, `contentTypeJSON`, `(*Server).today()`. Static files are served ahead of the mux for any path that
  cleans to theirs (`staticRoute`), so v2 must not register them.
- **Query strings.** `parseRackQuery` (rack_query.go) is a port of Rack 3.2's `parse_nested_query` with the limits
  Roda installs (depth 32, 4096 pairs, 4 MB): nil for a bare key, `*[]any` and `rackHash` for nested keys, an error on
  type conflicts at any depth. `parseV1Params` splits its top level into v1's string parameters. Roda's
  params_capturing parses the query before any matcher with arguments, so a malformed query fails with 422 even on a
  path no route matches; check whether V2 behaves the same way before reusing it (V2 may not use params_capturing).
  params_capturing also appends route captures to a `captures` query parameter (`v1Captures`): a string or hash there
  is a 422, an array's elements replace the path's dates.
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

`TestV1ResponsesMatchOpenAPI` also validates the Go responses to the corpus's v1 GETs against `v1/openapi.json`,
skipping those where Ruby's own answer breaks the document (a tiny amount rounds rates to 0, under its
`exclusiveMinimum`). `TestGoldenCoversCorpus` fails when the corpus and the golden file disagree, so a request added
without regenerating cannot go unchecked.

Corpus lines are `METHOD PATH [| Header: value]...`; date tokens `{today}`, `{tomorrow}`, `{latest}`, `{sunday}`,
`{bday:N}`, `{ago:N}` expand in Ruby, and the golden file records the expanded path, so Go never recomputes dates. To
extend: append lines (v2 requests go in a new section), add edge rows to the script if needed (they change the data
under every request, so rerun and review the whole diff), then regenerate:

From the repository root, with `$SCRATCH` any scratch directory:

```sh
DATABASE_URL=sqlite://$SCRATCH/api.sqlite3 APP_ENV=test \
  mise exec -- bundle exec ruby -Ilib -r./boot go/scripts/api_golden.rb 2026-09-29 \
  go/internal/api/testdata/corpus.txt go/internal/api/testdata/golden/api.json.gz
```

The database file must be a throwaway (the script migrates and reseeds it). The blended tables are empty after
`Fixtures.seed!`; if v2 needs materialized blends, have the script run `blend:rebuild` (and the rollup blends) after
the edge rows, as `blend_golden.rb` does. If v2 bodies need a looser comparison (CSV number formatting, say), extend
`checkGolden` per content type rather than loosening the JSON rules.

The v1 corpus (176 requests) covers the index, 404s, static files with HEAD/POST/OPTIONS/ranges and on uncleaned
paths (`/robots.txt/`, `/v1//openapi.json`), every v1 route with amounts (`to_f` quirks, near-tie rounding, infinite
and NaN results), bases, symbols (empty, unknown, expired, EUR), circular pairs, Rack query parsing (bad escapes, bare
keys, nested keys and type conflicts, invalid UTF-8), conditional requests (If-None-Match and If-Match across
methods), CORS preflights (allowed and denied methods), carry-forward across the holiday and the lone series, open,
closed, reversed, future, year-0 and empty intervals, and invalid dates. All match Ruby.

Verification found and fixed through the corpus: `rates.Round` rounded near ties differently from Ruby's `format`
(now an emulation of Ruby's dtoa fast path in `internal/rates/dtoa.go`, checked on 800,000 values), bare and nested
query keys, 422 on infinite or NaN rates, static files on uncleaned paths and OPTIONS caching, the If-Match and OPTIONS
cases of `r.etag`, invalid UTF-8 in `from`/`to`, and a malformed query on an unmatched v1 path.

The second verification found, outside the corpus: type conflicts below the top level of the query and Rack's
limits (now a full port, `parseRackQuery`), the `captures` query parameter params_capturing appends to, escaped paths
(`v1Raw`, raw paths in noindex and deprecation), empty Origin and Access-Control-Request-Method headers and NUL
paths in Rack::Cors preflights, and a stored row without a rate (now a 422, as Ruby's `amount * nil` raises; it was
skipped). The Ruby outcomes were recorded by running the same requests through the Ruby app into a scratch file;
`v1_captures_test.go`, `cors_test.go` and `rack_query_test.go` hold them. The golden file was not regenerated in that
pass, so fold the requests in `TestV1QueryCaptures`, `TestV1NestedQueryConflicts` and `TestV1EscapedPaths` into the
corpus the next time it is.

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
- A date that reaches Date.parse from a `captures[]` query value must be YYYY-MM-DD in Go; Ruby's Date.parse also
  takes forms like `20200101` or `Jan 1 2020`. Only the route's own YYYY-MM-DD captures reach it otherwise.
- V1's error handler turns every exception into 422. Go answers 422 for request errors (amount, currency pair, dates,
  %-encoding, a query `date=` on an interval route) and 500 for database failures.
- Only the query string feeds v1 parameters; Roda's indifferent params would also merge a form body. `upcase` is Go's
  simple case mapping (Ruby maps `ß` to `SS`).
- A success response whose body is first written after the deadline becomes a 500 JSON with `no-store` (Puma answers
  a raising body with a bare 500). JSON floats print shortest (`1` where Oj writes `1.0`); `Roundable` values over
  5000 are floats (Ruby returns Integers).
- Paths ServeMux would clean and redirect (`/v1//latest`) are routed as Roda routes them (`Server.rawPaths`, added
  at integration): 404 outside v2, v2's own router inside it. Escaped paths follow Roda too: the noindex and deprecation middleware read `URL.EscapedPath()`, and `v1Raw` keeps a path
  with a %-escape off the v1 routes (`/v1/%6Catest` is 404, as in Ruby). v2 routes need the same care.
- Static files carry no Last-Modified (embedded files have no mtime); ranges and conditional GETs still work.
- `Cache` treats empty credentials as unconfigured (Ruby checks only for nil, so an empty env var would try to purge).

## For the integrator

Done at integration (`cmd/frankfurter`). Wire the cache (the provider step left the hooks nil): in `cmd/backfill`, `c := cache.FromEnv()`, set
`provider.Ingester{..., Cache: c}`, and call `c.FlushPending(ctx)` after `BackfillTask` (Ruby's
`Cache.purge_pending(ignore_window: true)`); in `cmd/schedule`, pass the same kind of instance as `schedule.Deps.Cache`
and to the ingester. The blend and rollup tasks (`blend.RebuildAll` and friends) leave `Cache.purge` to their callers:
call `c.Purge(ctx)` after them as core-blend.md describes.
