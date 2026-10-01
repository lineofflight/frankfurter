# Core step: provider

Provider model, publishing calendar, backfill, the backfill task, the scheduler, provider health and heavy-compute
slots. `go test ./internal/provider ./internal/schedule ./internal/heavyslots ./cmd/...`
runs everything.

## Packages

### `internal/provider` (lib/provider.rb, lib/tasks/providers.rake)

- `Provider{Key, Name, DataURL, TermsURL, CoverageStart time.Time, PivotCurrency, RateType, CountryCode,
  PublishSchedule, PublishCadence, Frequency}`: one providers row. Empty string or zero time is NULL.
  - `ObservationFrequency()` (`frequency`, defaulting to daily), `Blends()`, `Lookback()` (`lookback_days`).
  - `UnknownCurrencies(ctx, q)`, `StartDate(ctx, q)` / `EndDate(ctx, q)` (stored text plus ok; as in Ruby a NULL
    coverage date reads as "" and wins the min), `LastSynced(ctx, q)` (zero when no rates).
  - `PublishesMissed(ctx, q, reference) (n, ok, err)`: ok false is Ruby's nil. `MissedSince(endDate, reference)` is
    the same with the end date given, which is what the spec stubs.
- `All(ctx, q)` (ordered by key), `Find(ctx, q, key)` (nil when absent). `LookbackDays`, `NonCurrencyCodes`.
  `Provider.non_blending_keys` and `Provider.seed` were already ported as `rates.NonBlendingKeys` and
  `rates.SeedProviders`.
- `Lookup(key, client)`: builds the registered adapter (`Provider#adapter`). Binaries import
  `internal/adapters/all`.
- `Ingester{DB, Client, Blend, Cache, Logger, Today, Adapter}`: `Provider#backfill`.
  - `Backfill(ctx, p)` uses Ruby's default cursor (`last_synced || coverage_start`); `BackfillAfter(ctx, p, after)`
    takes an explicit one (zero = from the source's start). Both log and swallow failures, as Ruby rescues.
  - The cursor is exclusive, so a cursor equal to `coverage_start` (a first backfill, or the full task) starts the day
    before to fetch that day too, and drops rows dated before `coverage_start` (LB's archive has one the day before
    its start). An explicit earlier cursor stores whatever the source returns. Same condition as Ruby (#739).
  - An adapter implementing `adapter.EachFetcher` walks its own windows (Ruby's `fetch_each` override); otherwise
    backfill uses `adapter.FetchEach`. BIS is the one implementer: it revisits the year before the cursor.
  - `adapter.FetchEach` starts each window on the previous window's `upto` rather than the day after it. An adapter
    that takes `after` as exclusive would otherwise never request `upto + 1`, losing a day at every window boundary
    (BCP lost 2001-01-01 and every 365th day after; BNR lost 2006-01-03, 2007-01-03, ...). Inclusive adapters refetch
    the shared day, which `ON CONFLICT DO NOTHING` skips. A one-day range still steps past `upto`, since only an
    inclusive adapter can use one and it would otherwise never advance.
  - Per batch: `rates.Reject`, `rates.Normalize`, drift warning for `Revises()` adapters, then one `BEGIN IMMEDIATE`
    transaction: insert (`ON CONFLICT DO NOTHING`), and when anything was inserted `rates.RefreshRollups`,
    `Blend.RefreshRollupsTx` (blending providers), `rates.RefreshSummaries`, `Blend.RefreshTx(min, max + 14)`
    (blending providers). After commit: `Cache.PurgeDebounced`, `PRAGMA optimize`.
  - `Adapter` overrides the registry (tests use fakes). `Today` defaults to `rates.Today`.
  - A write lock (an unexported mutex) serialises the backfill's writes, the batch transaction and `PRAGMA optimize`,
    across every backfill sharing the `Ingester`. Fetching, validation and reads (`LastSynced`, the drift check) run
    outside it, so network waits overlap. The scheduler and the backfill task each share one `Ingester`.
- `Blend` (`RefreshTx`, `RefreshRollupsTx`) and `Cache` (`PurgeDebounced`) are interfaces. `Blend` runs on the
  backfill's transaction (`q`), so it must not open its own. A nil `Ingester.Blend` means `Materialized`; a nil
  `Cache` is skipped until the cache step provides one.
- `Materialized{DB, Today}` is the real blend (`internal/blend`) behind both `provider.Blend` (`RefreshTx` =
  `blend.RefreshDaily`, `RefreshRollupsTx` = weekly and monthly `Rollup.Refresh`, joining the caller's transaction)
  and `schedule.Blend` (`Refresh`, `Ready` = `DailyReady`, `Rebuild` = `RebuildDaily`, `Populate`, each in its own
  transactions on `DB`).
- `BackfillTask(ctx, b Backfiller, providers, name, full, workers)`: the rake task. `cmd/backfill` wraps it.

### `internal/schedule` (bin/schedule)

- `Setup(r Registrar, Deps{Providers, Backfill, Blend, Cache, Today, Shuffle})` registers what bin/schedule
  registers: purge pending every minute, midnight re-blend of yesterday..tomorrow then purge, blend population every
  5 minutes (first in 30s, no overlap, unschedules itself when done), staggered startup backfills (2s apart, shuffled),
  and a no-overlap cron backfill per `publish_schedule`. Blend and cache jobs are left out when their dependency is nil.
- `Blend` here is `Refresh(ctx, from, to)`, `Ready`, `Rebuild`, `Populate(ctx, rates.Week|rates.Month)`; `Cache` is
  `PurgeDebounced`, `PurgePending`. They run their own transactions (unlike `provider.Blend`), hence the distinct
  method names: one blending type can implement both.
- `Scheduler` (`New(workers, log)`, `In`, `Every`, `Cron`, `Run(ctx)`) is the rufus-scheduler subset: a worker cap,
  no-overlap skipping, `Job.Unschedule`, failures and panics logged with the job kept. Cron uses gronx in the local zone.
  The binary sets the cap from `SCHEDULER_WORKERS` (default 16); `DB_POOL_SIZE` (default four per core, at least 20)
  sizes the DB pool.
- `DryRun(w, providers)` prints `startup: backfill[key]` and `cron: <expr> backfill[key]` lines.

### `internal/heavyslots` (lib/heavy_slots.rb)

`New(limit)`, `TryAcquire`, `Release` (never below zero), `Held`, `Max`, `DefaultMax` (`MAX_HEAVY_COMPUTES`, default
one per core and at least 2, where Ruby's was 2 per Puma worker; an invalid value panics at startup like Ruby's
`Integer()`), `ErrBusy`, `RetryAfterSeconds`. The API step uses it.

### Binaries

- `cmd/schedule [--dry-run]`: bin/schedule. Runs up to `SCHEDULER_WORKERS` jobs at once (default 16), independent of
  `DB_POOL_SIZE`, which sizes the DB pool.
- `cmd/backfill [-full] [provider]`: `rake backfill[provider]`; `FULL=1` also works.
- `cmd/providerhealth`: bin/provider_health.rb (API, REPO, DRY_RUN env as in Ruby). Its issue body matches Ruby byte
  for byte (`testdata/body.txt`).

## For later steps

- **Cache**: implement `provider.Cache` / `schedule.Cache`, wire them the same way, and call `PurgePending` with
  ignore-window at the end of `cmd/backfill` (Ruby's `Cache.purge_pending(ignore_window: true)`).
- **API**: `/v2/providers` needs `StartDate`, `EndDate`, `PublishesMissed(ctx, q, today)` and `UnknownCurrencies`.
- **Integrator**: once `internal/adapters/all` is regenerated with every adapter, port spec/provider_spec.rb's
  "resolves all seeded providers" (for every row of `provider.All`, `adapter.Lookup(p.Key)` succeeds) into
  `internal/adapters/all`. It cannot pass before then (four adapters registered today). A one-off check found an
  adapter package for every seeded provider key.

## Deviations

- Cron is evaluated in UTC for `publishes_missed`. Ruby builds UTC times but fugit evaluates the cron in the process's
  local zone, so its counts shift on a machine not in UTC (CEST counts a Sunday for a Monday 00:00-02:00 schedule).
  `testdata/publishes_missed.json`, recorded under `TZ=UTC` by `go/scripts/publishes_missed.rb`, pins 67,760 Ruby
  results across every seeded schedule and cadence; all match.
- Backfill's transaction is `BEGIN IMMEDIATE` (Ruby: a deferred transaction that the blend refresh then upgrades).
  Same outcome, no lock-upgrade failures under concurrent backfills.
- Inserted counts come from `RowsAffected` per row instead of `total_changes()`.
- The scheduler runs up to `SCHEDULER_WORKERS` jobs at once (default 16). Ruby caps rufus at the pool size
  (`max_work_threads: DB.pool.max_size`, i.e. `MAX_THREADS`) because its threads block on connection checkout and the
  GVL makes more useless. A backfill mostly waits on its source, so on a cold start the pool-sized cap queued providers
  and the fill took the sum of the queue rather than about the slowest provider. Writes instead queue on the
  `Ingester`'s write lock, since SQLite admits one writer and uncoordinated `BEGIN IMMEDIATE`s could run past the busy
  timeout behind a long batch.
- Log lines are slog records with attributes (`provider`, `count`, `detail`, `error`) instead of `"KEY: message"`
  strings; floats in the drift detail print shortest (`stored 1` where Ruby says `stored 1.0`).
- The dry run lists every provider, as Ruby does, whether or not its adapter is registered; backfilling a provider
  without one logs "no adapter registered" and moves on.
- Ruby's scheduler specs spawn `bin/schedule` and stub rufus; Go tests record `Setup`'s registrations with a fake
  `Registrar` and test `Scheduler` separately. The first two grouped-blend startup specs run against `Materialized`
  over the spec fixture (a wrapper fails monthly population once); the third stubs readiness and rebuild, as Ruby does.
- The grouped blend ingestion specs (spec/blended_rollup_spec.rb) also run here, in `ingest_test.go`, against the real
  backfill and blend. Ruby stubs a grouped refresh or `refresh_batch` to fail; Go makes the insert fail with a SQLite
  trigger on the blended table (for the batch case, on the newest week, which lands alone in the second batch of 100).
- `MAX_HEAVY_COMPUTES` parses like `Integer()`: surrounding space, `0x`/`0o`/`0b`/leading-zero octal, underscores.
- `provider_health`'s flagged sort is stable (Ruby's `sort_by` is not; the specs only use distinct counts).

## Specs ported

provider_spec (all but "resolves all seeded providers", see above), provider_health_spec, schedule_spec,
providers_task_spec, heavy_slots_spec, and blended_rollup_spec's "Grouped blend ingestion" against the real backfill.
