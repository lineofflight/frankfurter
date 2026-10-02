# Core step: domain

Rate and currency domain over SQLite, plus the shared test fixture. Everything here is tested against the Ruby specs
listed at the end; `go test ./internal/{db,seeds,currency,rates,fixtures,applog}` runs them.

## Packages

### `internal/seeds` (db/seeds)

`data/` is a byte-for-byte copy of `db/seeds` (go:embed cannot reach outside the module). After editing `db/seeds`, run
`go generate ./internal/seeds`; `TestDataMatchesRepository` fails while the copies differ.

- `FS fs.FS`: the seed tree.
- `Providers() ([]Provider, error)`: provider files sorted by name. Empty string = key absent (stored as NULL);
  `Frequency` defaults to `"daily"`.
- `Pegs()`, `DefunctCurrencies()`, `NascentCurrencies()`, `CurrencyPatches()`: typed parsers. Prefer the `currency`
  accessors, which cache.

### `internal/currency` (currency_patches, defunct_currency, nascent_currency, peg, peg_anchor, currency)

Pure reference data, plus the catalogue reads.

- Money gem: `Find(code) (Info, bool)` is `Money::Currency.find` (case-insensitive, aliases included: `Find("GHC")`
  gives `ISOCode "GHS"`). `Named(code)`, `Codes()` (every table id, the `named_currencies` list). `Info` has
  `ISOCode, Name, Symbol, ISONumeric, SubunitToUnit`; an empty `Symbol`/`ISONumeric` is Ruby's nil (the API should emit
  null). The table is `money.json` (the gem's table dumped by `go/scripts/dump_money.rb`) with
  `currency_patches.json` merged in at load; `testdata/patched.json` is Ruby's post-patch table and a test checks all
  234 entries match. Regenerate both after a money gem upgrade (commands in the script header).
- `DefunctCurrencies()`, `FindDefunct`, `Expired(code, date)`; `NascentCurrencies()`, `FindNascent`,
  `Premature(code, date)`.
- `Pegs()` (sorted by seed file), `FindPeg(quote)`.
- `AnchorPegs(rows []Blended, base string) []Blended` is `PegAnchor.apply`. `Blended{Date, Base, Quote, Rate,
  Providers []Contribution}` with `Contribution{Key, Date, Rate, Excluded}`; `Providers == nil` means Ruby's row with no
  `:providers` key (synthesised). The blending step should produce `[]currency.Blended` (or convert) so it can call this.
- Catalogue (`Currency` model): `All(ctx, q)`, `Active(ctx, q, today)`, `FindCurrency(ctx, q, code)` (nil when
  absent), `WithProviders(ctx, q, keys)`, `Providers(ctx, q, code)`. `Currency{ISOCode, StartDate, EndDate, Peg *Peg}`
  with `Name()` and `Metadata()`; peg merging and widening as in Ruby. `to_h`/`to_h_with_providers` are left to the API:
  it has every field it needs here.

### `internal/rates` (rate, rate_scopes, rate_spike, rate_components, rate_precision, rate_validation, currency_summary, roundable, bucket, carry_forward, Provider.seed and refresh_rollup)

Scopes are SQL text with constants inlined via `db.Lit`, so they compose like Sequel datasets.

- Tables: `Daily`, `Weekly`, `Monthly` (`Table{Name, DateColumn, Precision}`), `Tables`, `Rollups`.
- `Query{Table, From, Select, Where, Order}` with `Filter`, `Columns`, `OrderBy`, `Condition`, `SQL`. `t.Dataset()` is
  the whole table. Scopes:
  - `t.Blendable(f)`: named currencies, provider not in `f.NonBlending`, before terminal dates, and daily rows not
    flagged in `rate_spikes`; rollups instead go through `eligible_rollups`, which recomputes a pair's average when an
    expired or spiked observation contaminates its bucket (a subquery aliased to the table name, exactly Ruby's shape;
    the weekly coverage query still uses the covering index, see `TestWeeklyCoverageUsesCoveringIndex`).
    `LoadBlendFilter(ctx, q)` reads the `BlendFilter{NonBlending, Spikes}`: `NonBlendingKeys(ctx, q)` and whether
    `rate_spikes` exists, since migrations before 045 blend through the scope too (Ruby's `RateScopes.spiked` guard).
  - `q.Between(start, end, today)`: Ruby's snap-back range, ordered by date, quote.
  - `q.Only(currencies...)`: pivot-currency pairs, via a join on providers; select list `table.*`.
  - `q.Downsample(p) string`: full SQL; rows of base, provider, quote, rate, date (bucket text).
  - Building blocks: `NamedCondition()`, `ExpiredCondition(col, p)`, `CurrentCondition(col, p)`, `SpikedCondition()`
    (names no table, so it holds when coverage re-aliases rates).
- Buckets: `WeekBucket`, `MonthBucket`, `BucketSQL(p, expr)` are `Bucket.week/month/expression`; `Bucket(p, t)` is the
  same computation in Go (tested against SQLite for every day of 2016-2017). `SpanSQL(p, bucket, date)` is
  `Bucket.span`, a date range around a bucket so `eligible_rollups` can seek the date index.
- Spikes (`RateSpike`): `DetectSpikes(rows Query) Query` flags an observation `SpikeFactor` (3) times off both
  neighbours of its pair, which agree, within `SpikeMaxGapDays` (14); `RefreshSpikes(ctx, q, provider, dates)`
  rescreens the window around inserted dates and returns the dates whose flags changed, sorted.
- Precision and components: `Digits`, `Normalize`, `PrecisionSQL`, `Midpoint(bid, ask *float64)`,
  `ComponentsOf(adapter.Rate) Components{Mid, Bid, Ask}` (what to write; `rate` is generated), `Round` (Roundable).
  `Normalize` and `Round` round as Ruby's `format` does: the shortest decimal that round-trips, half to even, not the
  exact double (`Round(214.415)` is 214.42 and `Round(643.965)` is 643.96, where strconv gives the opposite).
  `testdata/ruby_format.txt` pins 4,000 Ruby results. Don't swap in `strconv.FormatFloat(v, 'f', n, 64)`.
- Validation: `Reject(records, leadDays, today) []adapter.Rate` (filters in place; relabels premature codes),
  `Horizon`, `MaxFutureDrift`, `Purge(ctx, *sql.DB, today, leads) (PurgeTotals, error)` in one `BEGIN IMMEDIATE`
  transaction, `ProviderLeads(ctx, q)` (leads of seeded providers whose adapter is registered; import
  `internal/adapters/all` in the binary so every adapter is).
- `RefreshSummaries(ctx, q, codes, provider)`: `CurrencySummary.refresh`; `provider == ""` means unscoped.
- `RefreshRollups(ctx, q, provider, dates) (map[Precision][]string, error)`: the provider-rollup half of
  `Provider#refresh_rollups`; returns touched buckets so the blending step can refresh `blended_weekly_rates` and
  `blended_monthly_rates`. `Buckets(ctx, q, provider, p, dates)` is `Provider#buckets`, which backfill uses to add the
  buckets of rescreened dates. `RebuildRollups(ctx, q)` rebuilds both tables from scratch.
- `SeedProviders(ctx, *sql.DB)`: `Provider.seed` / `db:seed`, including the recognised-exclusions restore.
- `Row{Date, Base, Quote, Provider, Rate}`, `Select(ctx, q, query, args...)` (columns date, base, quote, provider,
  rate), `CarryForward(rows, date, lookback)`, `EachSnapshot(rows, dates, lookback, yield)`, `LookbackDays`. A row
  whose stored components resolve no rate (a single published side) reads back with `Rate` NaN, Ruby's nil; blending
  and the API must skip or propagate it, never treat it as zero.
- `Today()`: Ruby's `Date.today` (local date at UTC midnight). Domain functions take `today` explicitly instead of
  reading the clock, which replaces Ruby's `Date.stub` in tests.

### `internal/fixtures` (spec/fixtures.rb)

- `New(t) *sql.DB`: a seeded database of the test's own. The fixture is generated once per test binary (VACUUM INTO
  a snapshot held in memory) and written to `t.TempDir()` per call, so tests mutate freely and may run in parallel. It
  stands in for spec/helper.rb's `Fixtures.seed!` plus the per-test rollback.
- `Seed(ctx, conn)`: `Fixtures.seed!` on any database (providers, 8840 generated rates, rollups, currencies, coverages).
  Verified row-for-row and bit-for-bit against Ruby's seeded tables (rates, weekly/monthly rollups, currencies,
  coverages, providers).
- `Today()` (fixed at first use), `LatestDate()`, `BusinessDay(daysAgo)`, `RecentSunday()`, `PrecedingFriday(d)`,
  `GapBoundaryMonday(daysAgo)` (Ruby's default argument is 60), `BusinessDays`.

### `internal/applog` (log.rb)

`Setup()` installs a slog text handler on stdout, info level, errors only under `APP_ENV=test`. `New(w, env)` for tests.

### Additions to `internal/db`

`querier.go`: `Querier` (common interface of `*sql.DB`, `*sql.Tx`, `*sql.Conn`), `Immediate(ctx, db, fn)` (BEGIN
IMMEDIATE transaction on one connection, like Sequel's `mode: :immediate`), `Lit`, `LitDate`, `LitList` (empty list
renders `(NULL)`: drop NOT IN conditions for empty lists), `NullDate` (scans DATE columns as time.Time or text).
`path_test.go` ports spec/db_spec.rb against `DefaultPath`.

## Deviations

- Ruby's specs run in a rolled-back transaction on one shared seeded database; Go tests get their own copy via
  `fixtures.New`.
- `adapter.Rate` has no "has a :mid key" notion: `ComponentsOf` treats a row with any of bid/ask/mid as having
  components (keeps its mid, even nil) and a row with none as a legacy rate. Ruby's `prices(bid: nil, ask: nil, mid: nil)`
  would store a nil mid where Go stores the rate; no adapter does that.
- `Reject` sees NaN where Ruby sees a nil rate; typed dates make Ruby's "accepts a string date" moot.
- Blended rollup tables (`BlendedWeeklyRate`/`BlendedMonthlyRate`) belong to the blending step. The rate_scopes
  "keeps blends identical" cases compare the blend's entire input (every blendable rollup row in the bucket) before and
  after instead of the materialised blend, and "omits pairs whose bucket contains only expired observations" skips
  its assertion on the blended table. The blending step should restore both against `blended_*_rates`.
- Ruby's `format("%.12g")` also treats a few 16- and 17-digit values that sit just short of a tie as ties (2 of
  20,000 random samples); `Normalize` treats only exact decimal ties that way. Those values then differ at the 12th
  significant digit (relative 1e-12), well within tolerance.
- `SeedProviders` runs in one transaction (Ruby: delete, then a multi-insert transaction, then the refresh).
- `Currency.find` is `FindCurrency` (the name `Find` is the Money lookup).

## Specs ported

db_spec, rate_spec (CarryForward, between, only, downsample), rate_scopes_spec, rate_spike_spec (`.detect` and the
late-arrival `refresh`, in `spike_test.go`; its query and backfill cases live in `internal/ratequery/spike_test.go` and
`internal/provider/spike_test.go`), rate_components_spec,
rate_precision_spec, rate_validation_spec, currency_spec, currency_patches_spec, currency_summary_spec,
defunct_currency_spec, nascent_currency_spec, peg_spec, peg_anchor_spec, grouped_coverage_index_spec.

Beyond the specs, a one-off parity run compared Ruby and Go on the fixture plus about 3,000 edge rows (every
terminal-date boundary, aliases, unknown codes, pre-euro dates, non-blending and lead providers, future rows and
buckets, stored rollups without dailies, blended buckets). Matching results: `Blendable` for all three tables,
`Between` (raw and blendable, rollups included, order too), `Downsample`, `Only`, `Purge` (totals and every table
afterwards, blends included), `RefreshSummaries` scoped and unscoped, and the catalogue reads (`All`, `Active`,
`WithProviders`, `FindCurrency`, `Providers`).

Not ported here, because they need the V2 query layer (RateQuery): `lib/rate_coverage.rb` (a RateQuery mixin that
calls `raw_dataset`, `each_snapshot`, `acquire_slot!` and friends) and `spec/reciprocal_consistency_spec.rb` (four HTTP
contract tests against `Versions::V2`). The API step should port both; its building blocks (`Query`, `Blendable`,
`EachSnapshot`, `AnchorPegs`, `Round`) are here.
