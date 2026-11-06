# Core step: blend

Blending and the materialized blends, plus the blend, rollup and consensus maintenance tasks. `go test
./internal/blend` runs the ported specs and a golden check of the tasks against Ruby.

## Package `internal/blend`

Ports weighted_average, consensus, blender, base_conversion, blended_rate, blended_rollup, blended_weekly_rate,
blended_monthly_rate, Provider#refresh_rollups (the blended half), and lib/tasks/{blend,rollups,consensus}.rake plus
db.rake's `purge_invalid`. bucket, carry_forward, weekly_rate and monthly_rate were already ported by the domain step
(`rates.BucketSQL`, `rates.CarryForward`/`EachSnapshot`, `rates.Weekly`/`rates.Monthly`); this step adds only their
spec, `internal/rates/carryforward_spec_test.go`.

Every function that reads the clock in Ruby (`Date.today` caps WeightedAverage's reference date) takes `today`.

### Blending (pure)

Input rows are `rates.Row` (as `rates.Select` reads them); output is `[]currency.Blended`, ready for
`currency.AnchorPegs`.

- `Blend(rows, base, today)` is `Blender#blend`; `Outliers(rows, base)` is `Blender#outliers`.
- `Convert(rows, base)` is `BaseConversion#convert` (per-provider bridging, first-occurrence bridge, reconcile of
  duplicate bridges).
- `ConsensusOutliers`, `ConsensusFind`, `Annotate` (returns `[]Annotated{Row, Excluded}`) are `Consensus#outliers`,
  `#find`, `#annotated`. Constants `MinProviders`, `MADMultiplier`, `MinDeviation`.
- `WeightedAverage(rows []Annotated, today)`; constants `DecayGraceDays`, `DecayRate`.
- Sums use Ruby's Kahan-Babuska `Array#sum` (`kbSum`), so given the same row order the floats are bit-identical to
  Ruby's. The golden check below matches every stored value exactly, not just within 1e-9.

### Materialized blends

All take a `db.Querier`. Given a `*sql.DB` they open their own `BEGIN IMMEDIATE` transactions (per chunk or batch, as
Ruby does); given anything else (a `*sql.Tx`, or the `*sql.Conn` inside `db.Immediate`) they join that transaction,
grouped batches under a savepoint. That is Sequel's `transaction(**(in_transaction? ? {} : {mode: :immediate}))`.

- Daily (`blended_rates`): `RefreshDaily(ctx, q, start, end, today)` (`BlendedRate.refresh`, 3-month chunks with Ruby's
  `Date#>>` clamping), `RebuildDaily(ctx, q, today)`, `DailyReady(ctx, q)`. `Pivot = "USD"`, `ChunkMonths = 3`.
- Grouped: `Rollup{Table, Source}` with `Weekly`, `Monthly`, `Rollups`. Methods `Refresh(ctx, q, buckets []string,
  today) (rows written, error)` in batches of `BatchBuckets` (100), `Rebuild`, `Populate` (returns rows written; zero
  means nothing changed), `Ready`, and `Read(ctx, q, start, end, today) (rows, ok, err)`: the stored pivot-base rows for
  the source buckets `Between` selects, `ok == false` when any bucket is unmaterialized (the API falls back to live).
  On a `*sql.DB`, `Read` uses one deferred read transaction, so coverage and values come from one snapshot. Buckets
  are stored date text, as `rates.RefreshRollups` returns them.
- `RefreshProviderRollups(ctx, q, provider, dates, today)` is `Provider#refresh_rollups` with no rescreened dates:
  `rates.RefreshRollups` plus, for blending providers, the grouped refreshes of the touched buckets. Backfill adds the
  buckets of rescreened dates itself (`provider.Ingester`).

### Tasks

The Ruby tasks call `Cache.purge`; the Go functions leave that to the caller (the cache step), with the same rule: purge
after `RebuildAll`, and after `RebuildProviderRollups`/`PurgeInvalid` even when they return an error (their source
changes may already be committed; `PurgeInvalid` returns non-zero totals alongside a rebuild error for that reason).

- `RebuildAll(ctx, conn, today)`: `blend:rebuild`.
- `RebuildProviderRollups(ctx, conn, provider, today)`: `rollups:rebuild[provider]`; empty provider rebuilds all; the
  key matches case-insensitively and an unknown one is an error. Source rebuild and grouped invalidation commit
  together; the refill runs afterwards.
- `PurgeInvalid(ctx, conn, today, leads)`: `db:purge_invalid` (`rates.Purge`, then `Populate` both grouped tables, then
  `RebuildDaily`). `leads` comes from `rates.ProviderLeads`.
- `ScanConsensus(ctx, q, from, to, today)` (zero bounds mean all history), `ScanRecentConsensus`, `ScanYearConsensus`:
  `consensus`, `consensus:recent`, `consensus[year]`. They log like the task and return a `ConsensusReport`.

### What the backfill (provider) step needs

`Provider#backfill`'s transaction, per fetched batch, after inserting: `RefreshProviderRollups(ctx, tx, key, dates,
today)`, `rates.RefreshSummaries`, and for blending providers `RefreshDaily(ctx, tx, minDate, maxDate+LookbackDays,
today)`, all inside one immediate transaction; purge the cache only after it commits. `rollup_test.go`'s `ingest`
helper is exactly that shape; the provider step should run the four "Grouped blend ingestion" tests against its real
backfill.

## Golden check

`go/scripts/blend_golden.rb` seeds the spec fixture with `Date.today` pinned (2026-09-29), adds edge rows (a
contributor aging out of the lookback, a consensus-masked outlier, an outlier on its cohort's own date, duplicate
bridges, a quarterly non-blending provider, future-dated rows, a pegged quote with provider rows, bid/ask-only rows, a
defunct currency across its terminal date, an unknown code), then runs four stages, dumping every rollup and blend table
after each into `testdata/golden/tasks.json.gz`:

1. `rollups:rebuild`
2. `blend:rebuild`
3. `rollups:rebuild[ecb]` after ECB's GBP rates move 1%
4. `db:purge_invalid` after rows beyond the future horizon are inserted and rolled up (this stage also dumps `rates`
   and the task's totals line; Ruby's adapter leads are recorded, since the Go test registers no adapters)

It also records the consensus scan of the seeded rates: the `consensus` task's own total line and the per provider and
quote counts, and, after stage 2, `BlendedRollup.read` over nine ranges per grouped table (snap-back to the nearest
earlier bucket, across the gap after an isolated old series, a start after today, a range before any data, all history)
plus one range with an in-range bucket deleted inside a rolled-back transaction (nil: live fallback). Note that
`Bucket.week` dates are year start plus `%W` weeks, so a weekly bucket can fall after the days it holds and a range
ending today can miss it; Go reproduces that. `TestGoldenTasks` loads the same input rates into a fresh database and runs the Go tasks: every table at
every stage matches Ruby row for row, every rate bit-identical (the test checks 1e-9 relative), the consensus
and purge totals agree, and `Rollup.Read` returns Ruby's rows (or fallback verdict) for every recorded range.

Regenerate (throwaway database in the scratchpad; the script migrates it in a child process, because an in-process
migration defines `Provider` before its `frequency` column exists and Ruby then blends non-daily providers):

```sh
DATABASE_URL=sqlite://$SCRATCH/blend.sqlite3 APP_ENV=test \
  mise exec -- bundle exec ruby -Ilib -r./boot go/scripts/blend_golden.rb 2026-09-29 \
  go/internal/blend/testdata/golden/tasks.json.gz
```

## Specs

Ported in full: weighted_average_spec, consensus_spec, blender_spec, base_conversion_spec, carry_forward_spec (in
`internal/rates`), blended_rate_spec, blended_rollup_spec, blended_rollup_maintenance_spec, grouped_rollup_lock_spec.

- Ruby stubs (`Blender.stub`, `refresh_batch` stubs, `define_singleton_method(:refresh_chunk)`, SQL loggers) become
  unexported test seams in `store.go` (`wrapChunk`, `beforeBatch`, `afterRead`, `afterSourceRead`, `beforeRefill`),
  no-ops in production. A failed blend is injected at the start of the batch, which is equivalent (nothing is written
  before the blend). Insert failures use a SQLite trigger, as the Ruby purge spec does.
- `FutureDate.stub(:horizon, d)` becomes `rates.Purge` with `today = d - MaxFutureDrift` and no leads.
- The ingestion cases use the local `ingest` helper (see above) instead of `Provider#backfill`, which the provider step
  owns.
- grouped_rollup_lock_spec: a second `*sql.DB` on the same file with a 100 ms busy timeout; its insert fails inside the
  source transaction and succeeds before the refill.
- rate_scopes_spec's two cases that assert on the grouped blend tables ("keeps blends identical when retained expired
  rows share a bucket", "omits pairs whose bucket contains only expired observations") also run here against the
  tables themselves (`scope_test.go`); `internal/rates` checks them through the blend inputs.

Partly ported, the rest needs the API step's `Versions::V2::RateQuery`:

- rollup_spec: parity with downsample, incremental refresh and shared scopes are ported. "boundary bucket inclusion"
  and "single-date grouped query" are checked at the table level through `Rollup.Read` (what a grouped RateQuery
  serves once ready); the API step should port the query-level versions. "cache key freshness" is not ported.
- blend_parity_carveouts_spec: the two carve-outs are checked at the table level in `carveout_test.go` (stored rows
  keep the canonical anchor-date value; consensus-masked observations are never stored). "verifies engineered
  divergences" (BlendParity's explain_divergence) and "blends range batches in the pivot frame" (RateQuery#derive) are
  not ported.
- blend_parity_spec and lib/blend_parity.rb (the `blend:parity` task) are not ported: BlendParity replays request
  shapes through RateQuery's table and live paths. The api_v2 step ported it (`ratequery.Parity`, `frankfurter
  blend-parity`).

## Deviations

- `rates.Row.Rate` is NaN for a stored row with a single published side. Ruby raises on the nil inside Blender; Go
  blends the NaN and the insert then fails on the NOT NULL rate column, so a refresh still errors.
- The consensus task counts per provider and quote. Ruby's `blender.outliers.each do |provider, quote|` destructures
  each outlier Hash into `provider = row, quote = nil`, so its combo labels are the whole row; totals agree.
- `Rollup.Read` returns `(rows, ok, err)` where Ruby returns nil or rows.
