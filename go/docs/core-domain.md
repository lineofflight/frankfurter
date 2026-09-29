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

### `internal/rates` (rate, rate_scopes, rate_components, rate_precision, rate_validation, currency_summary, roundable, bucket, carry_forward, Provider.seed and refresh_rollup)

Scopes are SQL text with constants inlined via `db.Lit`, so they compose like Sequel datasets.

- Tables: `Daily`, `Weekly`, `Monthly` (`Table{Name, DateColumn, Precision}`), `Tables`, `Rollups`.
- `Query{Table, From, Select, Where, Order}` with `Filter`, `Columns`, `OrderBy`, `Condition`, `SQL`. `t.Dataset()` is
  the whole table. Scopes:
  - `t.Blendable(nonBlending)`: named currencies, provider not in `nonBlending` (from `NonBlendingKeys(ctx, q)`),
    before terminal dates; rollups additionally go through `eligible_rollups` (a subquery aliased to the table name,
    exactly Ruby's shape; the weekly coverage query still uses the covering index, see `TestWeeklyCoverageUsesCoveringIndex`).
  - `q.Between(start, end, today)`: Ruby's snap-back range, ordered by date, quote.
  - `q.Only(currencies...)`: pivot-currency pairs, via a join on providers; select list `table.*`.
  - `q.Downsample(p) string`: full SQL; rows of base, provider, quote, rate, date (bucket text).
  - Building blocks: `NamedCondition()`, `ExpiredCondition(col, p)`, `CurrentCondition(col, p)`.
- Buckets: `WeekBucket`, `MonthBucket`, `BucketSQL(p, expr)` are `Bucket.week/month/expression`; `Bucket(p, t)` is the
  same computation in Go (tested against SQLite for every day of 2016-2017).
- Precision and components: `Digits`, `Normalize`, `PrecisionSQL`, `Midpoint(bid, ask *float64)`,
  `ComponentsOf(adapter.Rate) Components{Mid, Bid, Ask}` (what to write; `rate` is generated), `Round` (Roundable).
- Validation: `Reject(records, leadDays, today) []adapter.Rate` (filters in place; relabels premature codes),
  `Horizon`, `MaxFutureDrift`, `Purge(ctx, *sql.DB, today, leads) (PurgeTotals, error)` in one `BEGIN IMMEDIATE`
  transaction, `ProviderLeads(ctx, q)` (leads of seeded providers whose adapter is registered; import
  `internal/adapters/all` in the binary so every adapter is).
- `RefreshSummaries(ctx, q, codes, provider)`: `CurrencySummary.refresh`; `provider == ""` means unscoped.
- `RefreshRollups(ctx, q, provider, dates) (map[Precision][]string, error)`: the provider-rollup half of
  `Provider#refresh_rollups`; returns touched buckets so the blending step can refresh `blended_weekly_rates` and
  `blended_monthly_rates`. `RebuildRollups(ctx, q)` rebuilds both tables from scratch.
- `SeedProviders(ctx, *sql.DB)`: `Provider.seed` / `db:seed`, including the recognised-exclusions restore.
- `Row{Date, Base, Quote, Provider, Rate}`, `Select(ctx, q, query, args...)` (columns date, base, quote, provider,
  rate), `CarryForward(rows, date, lookback)`, `EachSnapshot(rows, dates, lookback, yield)`, `LookbackDays`.
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
  after instead of the materialised blend.
- The "keeps forward-dated rows from a provider that publishes ahead" purge case passes HMRC's 31-day lead directly;
  `ProviderLeads` is tested separately with the JPC adapter (7 days) registered.
- `SeedProviders` runs in one transaction (Ruby: delete, then a multi-insert transaction, then the refresh).
- `Currency.find` is `FindCurrency` (the name `Find` is the Money lookup).

## Specs ported

db_spec, rate_spec (CarryForward, between, only, downsample), rate_scopes_spec, rate_components_spec,
rate_precision_spec, rate_validation_spec, currency_spec, currency_patches_spec, currency_summary_spec,
defunct_currency_spec, nascent_currency_spec, peg_spec, peg_anchor_spec, grouped_coverage_index_spec.

Not ported here, because they need the V2 query layer (RateQuery): `lib/rate_coverage.rb` (a RateQuery mixin that
calls `raw_dataset`, `each_snapshot`, `acquire_slot!` and friends) and `spec/reciprocal_consistency_spec.rb` (four HTTP
contract tests against `Versions::V2`). The API step should port both; its building blocks (`Query`, `Blendable`,
`EachSnapshot`, `AnchorPegs`, `Round`) are here.
