# Core step: ops

Migrations, the single `frankfurter` binary (Procfile processes, rake tasks, bin scripts) and the container image.
`go test ./internal/migrate ./cmd/frankfurter` runs everything.

## Package `internal/migrate` (db/migrate, the db:migrate task)

The 40 migrations, applied through Sequel's own bookkeeping table (`schema_info`, one row, `version`). A database the
Ruby app migrated opens at its version and is left alone at 40; a database migrated here passes Ruby's
`Sequel::Migrator.check_current` (checked by hand both ways against the Ruby test database and a Go-built one).

- `Migration{Version, Name, Up, Down}`, `Migrations()`, `Latest()` (40), `ErrIrreversible` (008's down).
- `Up(ctx, conn)`, `To(ctx, conn, target)` (up or down, like `IntegerMigrator` with `target:`), `Current(ctx, q)` (0
  without a `schema_info` table), `CheckCurrent(ctx, q)`.
- Each migration and its version bump commit together in one `BEGIN IMMEDIATE` transaction; the first failure stops
  the run at the last good version.
- DDL is the SQL Sequel emitted on SQLite, table rebuilds included (031's `set_column_allow_null` and its down), so
  `sqlite_master` is identical to Ruby's at every version. `testdata/schemas.json` records Ruby's schema after each step
  up from 0 to 40 and down from 40 to 8, where 008 refuses; `go/scripts/migration_schemas.rb` regenerates it:

  ```sh
  DATABASE_URL=sqlite://$SCRATCH/schemas.sqlite3 APP_ENV=test \
    mise exec -- bundle exec ruby -Ilib -r./boot go/scripts/migration_schemas.rb \
    go/internal/migrate/testdata/schemas.json
  ```

- Data migrations reuse the domain packages: 025 `rates.PrecisionSQL`, rollups `rates.WeekBucket`/`MonthBucket`, 034
  `currency.Codes()` (the Money table, 234 codes, same as Ruby's), 036-040 and 037 `rates.RefreshSummaries`.
- `internal/db.Schema` (what `dbtest` and `fixtures` create) must stay equal to the migrated schema:
  `TestLatestMatchesEmbeddedSchema` fails when they drift. For a new Ruby migration: add the Go migration, regenerate
  `schemas.json`, and redump `internal/db/schema.sql` with its version.

## Binary `cmd/frankfurter`

`frankfurter <command> [flags] [args]`, run from the directory holding `db/` or with `DATABASE_URL`, like the Ruby app.
Rake and Procfile names work as aliases. Logging is `applog.Setup()` (slog text on stdout).

| Command | Ruby | Notes |
|---|---|---|
| `serve` (`web`) | Procfile web: puma + config.ru | PORT (default 8080); `(&api.Server{DB: conn}).Handler()`; graceful shutdown on SIGINT/SIGTERM. Puma workers and threads have no counterpart; MAX_THREADS sizes the DB pool. |
| `schedule [-dry-run]` (`scheduler`) | Procfile scheduler: bin/schedule | `schedule.Setup` with the real `Ingester` (blend defaults to `Materialized`), `provider.Materialized` and one `cache.FromEnv()` shared by backfills and purge jobs. |
| `start` | Dockerfile CMD: `rake db:setup && foreman start` | setup, then serve and schedule in one process, each with its own DB pool (as two processes had); either stopping stops both. |
| `migrate [-version N]` (`db:migrate`) | db:migrate, `VERSION=` | `migrate.To`. |
| `seed` (`db:seed`) | db:seed | `rates.SeedProviders`. |
| `setup` (`db:setup`) | db:setup | migrate, then seed. |
| `backfill [-full] [provider]` | `backfill[provider]`, `FULL=1` | `provider.BackfillTask` with a cached `Ingester`, then `Cache.FlushPending` (Ruby's `purge_pending(ignore_window: true)`). An unknown provider fails before either. |
| `blend-rebuild` (`blend:rebuild`) | blend:rebuild | `blend.RebuildAll`, then `Cache.Purge`. |
| `rollups-rebuild [provider]` (`rollups:rebuild`) | `rollups:rebuild[provider]` | `blend.RebuildProviderRollups`, then `Cache.Purge` even on error. |
| `consensus [year]` | `consensus[year]` | `blend.ScanConsensus` / `ScanYearConsensus`. |
| `consensus-recent` (`consensus:recent`) | consensus:recent | `blend.ScanRecentConsensus`. |
| `purge-invalid` (`db:purge_invalid`) | db:purge_invalid | `rates.ProviderLeads` + `blend.PurgeInvalid`; purges the CDN when totals are non-zero, even if a rebuild failed. |
| `purge-cache` (`cache:purge`) | cache:purge | `Cache.Purge`. |
| `healthcheck` | Dockerfile `curl -f --max-time 9 localhost:$PORT` | GET `/` on PORT; exit 1 on error or status >= 400. |

The binary imports `internal/adapters/all`, so every registered adapter is available to backfill and
`rates.ProviderLeads`.

Folded in: `cmd/schedule`, `cmd/backfill` and `cmd/server` are gone; their code and `TestDryRunReadsTheDatabase` live
here. `core-provider.md` and `core-api_v1.md` still name them; read those entries as `frankfurter schedule`,
`frankfurter backfill` and `frankfurter serve`. `cmd/providerhealth` stays a separate binary: bin/provider_health.rb is
a GitHub Actions job against the live API, neither a Procfile process nor a rake task, and has nothing to share with
the server. `cmd/genadapters` and `cmd/vcrconvert` are porting tools.

Test seams: `newCache` (the cache every command builds) and `today`. `backfillWith` and `scheduleDeps` take their
dependencies so tests check the wiring (deferred purge flushed at the end of a backfill, one cache shared by the
scheduler).

## Container: `go/Dockerfile`

`docker build -t frankfurter go/`. Two stages: `golang:1.27-alpine` builds with `CGO_ENABLED=0 -trimpath -ldflags="-s
-w"` (a static binary; seeds, static files, CA intermediates and PDFium's WebAssembly are embedded), and
`gcr.io/distroless/static-debian12` runs it (CA roots and tzdata included, no shell). `ENTRYPOINT frankfurter`, `CMD
start`; `docker exec <container> frankfurter backfill ecb` runs a task. It keeps the Ruby image's contract: WORKDIR
`/app`, database in `/app/db` owned by uid 1000, `APP_ENV=production`, `PORT=8080`, the same HEALTHCHECK timings.
`go/.dockerignore` keeps testdata, tests, docs and scripts out of the build context. Docker isn't installed here, so
the image itself is unbuilt; the same `go build` line cross-compiles to a static linux/amd64 ELF.

## Not ported

- `blend:parity` (lib/blend_parity.rb): needs the V2 RateQuery; listed as an API gap in core-blend.md.
- `bin/console` (IRB): no Go counterpart; use `sqlite3` on the database.
- `rake default`, `rake spec`, `rake rubocop` and the custom cops in `spec/rubocop`: Ruby lint and test tooling,
  intentionally not ported. Go's equivalents are `gofmt`, `go vet` and `go test`.
- `config/ca_bundles`: already embedded by the foundation step (`internal/adapter/cabundles`, trusted by
  `adapter.NewClient`). `config/puma.rb`: see `serve`.

## Specs ported

All in `internal/migrate/spec_test.go`, one database file per test instead of Ruby's child processes.

- currency_exclusions_migration_spec: `TestCurrencyExclusionsMigrationIndexesUnknownCodesAndRemainsReversible`.
- rate_components_migration_spec: `TestRateComponentMigrationsKeepRatesReadableThroughRollbackAndReapplication` (the
  standalone read uses a plain `sql.Open` connection).
- comesa_dollar_migration_spec: `TestComesaDollarMigrationPromotesStoredRBMRates`.
- bota/rba/bnm/rbm_sdr_migration_spec (3 its each): `TestSDRMigrationCompletesSetupAndPreservesRepairedHistory`,
  `TestSDRMigrationRollsBackConflictingComponents`, `TestSDRMigrationLeavesDatabasesWithoutHistoryAlone`, each with a
  subtest per provider. BOTA's "retained SDR rollup" check covers the quote side too, as the other three do.

The specs stub `Cache.purge` to fail, to prove setup never needs the CDN; Go's migrations and seed have no cache
dependency, so there is nothing to stub. Beyond the specs, a one-off run of the BOTA case with an extra February row
compared Ruby and Go after migrating: rates, weekly and monthly rollups, coverages, catalogue and exclusions were
identical.

`internal/migrate/migrate_test.go` adds the schema checks above, a file-for-file match with `db/migrate`, rollback of
001-007 to an empty database, and `CheckCurrent`. `cmd/frankfurter/main_test.go` covers dispatch, setup twice,
`VERSION`, the dry run, the scheduler and backfill cache wiring, which tasks purge, and the healthcheck.

## Deviations

- Migrations are transactional (Sequel on SQLite runs them bare). Same end states; a failure can no longer leave a
  half-applied migration.
- 027 and 028 recompute part of the daily blend in Ruby with today's blend code, which reads columns and tables those
  old schemas lack. Go clears `blended_rates` instead when the migration relabelled anything, handing the rebuild to
  the scheduler's existing `DailyReady`/`RebuildDaily` job, as 036-040 do. On databases with nothing to relabel (fresh
  ones, and production, which is past 040) both leave the blend alone.
- 003 deletes its seven providers in one statement; 027 rebuilds rollups over its scope without the redundant bucket
  list. Same rows.
- `VERSION` must be an integer (Ruby's `to_i` turns a typo into 0 and rolls everything back).
- `rollups-rebuild` purges the CDN even when its source transaction fails (Ruby purges only once that commits);
  purging without a change is harmless.
- `start` runs web and scheduler in one process rather than foreman's two.

## For the integrator

- Deploy scripts and workflows that call `bundle exec rake ...`, `bin/schedule` or puma switch to the commands above
  (`docker exec <container> frankfurter backfill`, `frankfurter blend-rebuild`, ...).
- `internal/api` was mid-edit by the api_v2 step while this step ran, so the package didn't compile in the shared tree.
  `cmd/frankfurter` was built and tested against the committed `internal/api` through a `go -overlay` that hid the
  uncommitted files. `serve` only needs `api.Server{DB}` and `Handler()`; if v2 adds a constructor or required fields,
  update `listen` in `serve.go`.
