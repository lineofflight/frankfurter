# Core step: api_v2

The v2 API (lib/versions/v2.rb), its rate query (lib/versions/v2/rate_query.rb), queryable coverage
(lib/rate_coverage.rb) and the blend parity harness (lib/blend_parity.rb). `go test ./internal/ratequery ./internal/api`
runs everything.

## Packages

### `internal/ratequery` (rate_query.rb, rate_coverage.rb, blend_parity.rb)

- `New(ctx, q db.Querier, params Params, opts Options) (*Query, error)`: `RateQuery.new`. Validation runs in Ruby's
  order (unknown parameters, dates, conflicting dates, group, expand, currencies, daily range cost), so the first
  failure is the same. Failures are `*ValidationError` (422; `IsValidation(err)`); a parameter Ruby cannot treat as a
  string (a nested Rack value, invalid UTF-8) is a plain error (500), as Ruby's NoMethodError/ArgumentError are.
- `Params` is `map[string]any` as Rack parses the query: string, nil (bare key, reads as absent), or nested
  (`api.parseRackQuery`'s `*[]any` and hashes). `ratequery.Params(rackHash)` converts directly.
- `Options{Today, Deadline, Timeout, Slots}`: `Date.today` (zero: `rates.Today()`), the compute deadline (zero: none;
  the API passes `RequestDeadline(r)`), the budget for the error message, heavy slots (nil: `DefaultSlots`,
  `heavyslots.New(heavyslots.DefaultMax)`, the process-wide `RateQuery.heavy_slots`).
- `(*Query).Each(ctx, yield func(Record) error) error` and `All(ctx)`: the records, in response order. Dispatch as in
  Ruby: grouped ranges from `blend.Weekly/Monthly.Read` when a chunk is fully materialized, else the provider rollups
  blended live; plain daily ranges and snapshots from `blended_rates` once `blend.DailyReady`, else live;
  providers= and expand=providers always live. Live daily ranges hold a heavy slot for the enumeration.
- `Range`, `DateRelative`, `ExpandProviders`, `CacheKey(ctx)` (MD5 of the raw max date and expand, the ETag),
  `CSVFilename`, `ReleaseSlot` (idempotent), `Base`, `Quotes`, `Providers`. `ForceLive` and `Coverage`
  (`RollupCoverage{Materialized, Fallback, Empty}`) are the parity harness's `force_live=` and `rollup_coverage`.
- `(*Query).History(ctx) (Coverage, error)`: `RateCoverage#coverage` (`/v2/coverage`). Named History because
  `Coverage` is the result type.
- Errors: `*DeadlineError{Timeout}` (RequestTimeout::Error raised by `check_deadline!`, 503), `BusyError`
  (HeavySlots::Busy, 503 + Retry-After; unwraps to `heavyslots.ErrBusy`).
- `Record{Date, Base, Quote, Rate Number, Providers []Contribution, HasProviders}` marshals with Ruby's key order.
  `Number{Value, Int}` keeps Roundable's Integer (values over 5000) apart from Floats; `Number.String()` and
  `RubyFloat` print Ruby's `to_s` (`1.0`, `1.0e-05`, `12345`), which CSV needs.
- `Parity(ctx, conn, samples, seed, today) (ParityReport, error)`: `BlendParity.run`. `ParityReport{Shapes,
  SnapbackRows, Failures, GroupedCoverage, Incomplete}` with `Passed()` and `String()` (the task's output).
  Random shapes come from `math/rand/v2` PCG, so a seed draws different shapes than Ruby's MT; the adversarial shapes
  are the same.

### `internal/api` (v2 files)

- `v2.go`: routes (registered from `init` on `/v2` and `/v2/`), Roda's type_routing (extension or Accept, with
  `Vary: Accept`), the 404 status handler and error handler (both clear the response's headers first), `/coverage`,
  `/providers/...`. Paths match raw (`EscapedPath`), as Roda matches PATH_INFO.
- `v2_rates.go`: `/rates`, `/rate/{base}/{quote}` and their provider aliases; JSON, streamed JSON arrays, NDJSON and
  CSV. `newRateQuery` and `v2Now` are test seams.
- `v2_catalog.go`: `/currencies`, `/currency/{code}`, `/providers`, `/providers/{key}`.
- `Server.HeavySlots` nil means `ratequery.DefaultSlots`.

### Addition to `internal/currency`

`presence.go`: `HasISONumeric(code)`, `HasSymbol(code)`. The Money gem holds `""` for some fields (GGP's
iso_numeric) and nil for others (a patch-registered CMD), and the API emits `""` and `null` accordingly; `Info`
cannot tell them apart. (core-domain.md's "empty ISONumeric is nil" holds only for patch-registered codes.)

## Golden corpus

`testdata/corpus.txt` now has 580 requests, 371 of them v2 (all routes, methods, extensions and Accept types; every
query shape: latest, dates across holidays, weekends and gaps, daily and grouped ranges, snap-back, reversed and
future ranges, the range cap, pegs and pegged bases, providers= single, several, unknown, non-blending and index
providers, expand=providers, passthrough, CSV/NDJSON/streamed JSON, conditional requests, coverage, catalogue and
provider metadata, malformed and nested parameters). The v1 requests the api_v1 verifier left as unit tests
(`TestV1QueryCaptures`, `TestV1NestedQueryConflicts`, `TestV1EscapedPaths`) moved into the corpus and the tests
were removed.

`go/scripts/api_golden.rb` changes:

- Edge rows for v2: ECB publishes AED (peg-snapped, contributors excluded), NB publishes USD/NOK and EUR/NOK plus the
  I44 index (with its currency summaries refreshed, so I44 is an exclusion), INFOREURO has two monthly EUR/USD rows.
- After the edge rows it runs `BlendedRate.rebuild` and both grouped rebuilds, so plain v2 shapes read the tables
  (the tables are dumped and loaded like the rest). providers= and expand=providers still exercise the live blend.
- NDJSON bodies are recorded as parsed lines (`ndjson`).

`golden_test.go` compares NDJSON line by line (JSON rules), compares `content-disposition`, and accepts any
`public, max-age=N, stale-if-error=86400` for a date-relative Ruby answer of that shape (N counts to the next UTC
midnight from when each side answered). CSV bodies compare exactly: Go prints numbers as Ruby does.
`TestV2ResponsesMatchOpenAPI` validates the Go answers to the corpus's v2 GETs (Accept headers included) against
`v2/openapi.json`; CSV is not in the document, and NDJSON, which the document types as a string, is checked line by
line against its `Rate` schema.

Verification pass: a regenerated golden file matched the committed one (bar the max-age countdown). A scratch
differential run then replayed about 16,000 generated v2 requests through both apps: random shapes and malformed
parameters, with the materialized blends rebuilt and with them empty (live path, range cap). It ran on the fixture plus
extra providers: BI (IDR, sub-1e-4 and over-5000 rates, gaps), FRED (weekly), RBA (bid/ask only), NBP (consensus
outliers, XDR) and LB (EUR to LTL base change). Every response matched, and every documented v2 JSON response conformed.
The generator lives outside the repository; the committed corpus is the durable check.

Regenerate exactly as core-api_v1.md describes (same command).

## Specs ported

versions/v2_spec, versions/v2/rate_query_spec, versions/v2/blended_rollups_spec, versions/v2/coverage_spec,
versions/v2/non_currency_rates_spec, versions/v2/provider_currencies_spec, and the gaps earlier steps left:
reciprocal_consistency_spec, blend_parity_spec, blend_parity_carveouts_spec (all four, at query level), rollup_spec's
"boundary bucket inclusion", "single-date grouped query" and "cache key freshness" at query level, and app_spec's
seven v2 cases. No `t.Skip`.

- v2_spec mounts `Versions::V2` alone; `newV2App` serves `routesV2` on a bare mux (no middleware), so exact headers
  such as `Vary: Accept` hold. app_spec's cases go through the full app.
- Ruby stubs become seams: `RateQuery.stub(:new)` is `newRateQuery`; `heavy_slots` is `Options.Slots` /
  `Server.HeavySlots`; `check_deadline!`, `emit_blended`, `Blender.new`, `BlendedRate.ready?` and grouped `read` are
  `Query.checkHook`, `Query.emitHook`, `blendRows`, `dailyReady` and `readRollup`; `@deadline` is `opts.Deadline`.
- An enumerator paused with `#next` is `Each` running in a goroutine blocked inside its first record. A client that
  disconnects mid-stream is a writer that fails after the first chunk: the compute stops and the slot comes back.
- "Grouped HTTP responses" stubs `Blender.new` to prove the table served; the Go test proves it by tampering with the
  stored blends afterwards and seeing the response change.
- `assert_conform_schema` is kin-openapi response validation against the named operation.

## Deviations

- ServeMux cleans paths before any handler runs: `/v2/rate//USD` and `/v2/providers//rates` get a redirect where Roda
  answers 422 and 404. The router itself follows Roda (an empty segment before a slash is captured;
  `TestV2CapturesEmptySegmentBeforeSlash`), so only the mux stands in the way; those two requests are not in the
  corpus.
- Streaming buffers 4 KB before writing through, and a failure after the first record aborts the connection
  (`http.ErrAbortHandler`), as Puma does when a body raises.
- Error messages keep Ruby's wording except for internal errors (Ruby's exception text); unknown parameters are
  listed sorted.
- `ParseDate` follows Ruby's Date for validity before 1582-10-15 (Julian leap years, the ten skipped days invalid),
  but builds the time.Time as a Gregorian date; no rates exist then, so only validity is observable.
- Provider lookups (`Provider[key]`) query the database per request; Ruby's static cache is not needed.

## For the integrator

- `blend:parity` has no binary yet (the ops step owns `cmd/`): it is `ratequery.Parity(ctx, conn, samples, seed,
  rates.Today())`, printing `report.String()` and failing unless `report.Passed()`; the Ruby task refuses to run while
  `blended_rates` is empty (default samples 200, seed 42).
- `cmd/frankfurter`'s server needs nothing new: v2 registers itself when `internal/api` is imported.
