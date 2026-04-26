# Frankfurter

Frankfurter is a free and open-source currency data API built with Ruby that tracks reference exchange rates from 20+ institutional sources (central banks, the IMF, the Federal Reserve, etc.).

## Architecture

- Roda web framework with Rack middleware
- SQLite with Sequel ORM (WAL mode)
- Unicorn
- Rufus scheduler for background data updates
- Cloudflare CDN with cache purge on import

## Project Structure

```
lib/
├── app.rb                    # Main Roda app — mounts v1 and v2
├── base_conversion.rb        # Rebases rates from any base to a common base
├── blender.rb                # Blends multi-provider rates: rebase → consensus → weighted average
├── bucket.rb                 # Shared SQL bucket expressions for weekly/monthly aggregation
├── cache.rb                  # Cloudflare cache purge
├── carry_forward.rb          # Carries forward most recent provider rate within a lookback window
├── consensus.rb              # Cross-provider outlier detection (MAD-based)
├── currency.rb               # Currency model (materialized from rates)
├── currency_coverage.rb      # CurrencyCoverage model (provider-currency join)
├── db.rb                     # Database configuration
├── historical_currency.rb    # Registers historical ISO 4217 codes via Money::Currency
├── log.rb                    # Shared logger
├── monthly_rate.rb           # MonthlyRate model on monthly_rates rollup table
├── peg.rb                    # Currency peg definitions (from db/seeds/pegs.json)
├── provider.rb               # Provider model: identity, backfill
├── provider/
│   ├── adapters/
│   │   ├── adapter.rb        # Abstract adapter: fetch interface, chunked iteration
│   │   └── <key>.rb          # One adapter per provider (auto-discovered)
│   └── adapters.rb           # Auto-requires all adapters
├── rate.rb                   # Rate model on rates table
├── rate_scopes.rb            # Shared dataset scopes for rate tables (rates, weekly, monthly)
├── roundable.rb              # Currency-aware decimal rounding
├── weekly_rate.rb            # WeeklyRate model on weekly_rates rollup table
├── weighted_average.rb       # Recency-weighted averaging with exponential decay
├── scheduler/
│   └── daemon.rb             # Forks and monitors the scheduler process
├── versions/
│   ├── v1.rb                 # Legacy API (ECB-only, frozen)
│   ├── v1/                   # V1 internals (quotes, query, rounding, currency names)
│   ├── v2.rb                 # Multi-provider API
│   └── v2/
│       └── rate_query.rb     # V2 rate query builder (blending, filtering)
├── public/
│   ├── root.json             # Root index document
│   ├── v1/openapi.json       # V1 OpenAPI spec
│   └── v2/openapi.json       # V2 OpenAPI spec
└── tasks/
    ├── consensus.rake        # Consensus scan across providers
    ├── db.rake               # Database migrations and setup
    ├── default.rake          # Default task (lint + test)
    ├── providers.rake        # Dynamic backfill task for all providers
    ├── rollups.rake          # Rebuild weekly/monthly rollup tables
    ├── rubocop.rake          # Linter task
    └── test.rake             # Test suite task

spec/                         # Minitest test suite
db/migrate/                   # Sequel migrations
db/seeds/
    ├── pegs.json             # Currency peg definitions
    └── providers/            # One JSON file per provider (e.g. ecb.json, boi.json)
```

## Key Components

### Adapters (lib/provider/adapters/)
- `Provider::Adapters::Adapter`: Abstract base class — `fetch` interface, `fetch_each` for chunked iteration, sleep no-op in test env
- Adapters are pure data extraction: they know how to talk to an external API and parse its response
- No identity — adapters have no `key` or `name`. Provider model owns identity.
- Optional class methods: `def backfill_range = N`, `def api_key = ENV[...] || raise(ApiKeyMissing)`
- Auto-discovered from `lib/provider/adapters/` via loader

### Models
- `Rate`: Sequel model on `rates` table. Scopes via `RateScopes`: `latest(date)`, `between(interval)`, `only(*quotes)`, `downsample(precision)`
- `WeeklyRate`, `MonthlyRate`: Rollup models on `weekly_rates` / `monthly_rates`, share scopes via `RateScopes`
- `Currency`: Sequel model on `currencies` table. Materialized from rates during backfill. Tracks global date ranges per currency.
- `CurrencyCoverage`: Join model on `currency_coverages` table. One row per (provider, currency) with per-provider date ranges. Belongs to Provider and Currency.
- `Provider`: Sequel model on `providers` table (seeded from `db/seeds/providers/*.json`). Static cache.
  - `#adapter`: finds adapter by convention (`Provider::Adapters.const_get(key)`)
  - `#backfill`: incremental backfill — starts from `last_synced` or `coverage_start`, delegates to `adapter.fetch_each`, filters excluded quotes, stamps provider key, upserts to DB, refreshes currency summaries
  - `#start_date`, `#end_date`: derived from currency coverages
  - `many_to_many :currencies` through `currency_coverages`
- `Peg`: Value object for currency pegs (from `db/seeds/pegs.json`)

### Blending Pipeline
- `Blender`: orchestrates rebase → consensus → weighted average
- `BaseConversion`: rebases rates from each provider's native base to a common base via inversion or cross rates
- `Consensus`: MAD-based outlier detection — flags rates that deviate significantly from the cross-provider median
- `WeightedAverage`: recency-weighted averaging with exponential decay past a grace period

### API (lib/app.rb)
- V1 at `/v1/*` — frozen legacy, ECB-only
- V2 at `/v2/*` — multi-provider with blended rates
- CORS enabled for all origins
- OpenAPI specs served as static files at `/v1/openapi.json` and `/v2/openapi.json`

### Scheduler (bin/schedule)
- Calls `provider.backfill` directly on Provider model instances
- Staggers startup backfill for all providers (2s apart)
- Cron schedule read from `publish_schedule` in the providers table (5-field cron; `null` for historical-only providers)
- Convention: poll every 30 min across a 3-hour window starting at the publish hour (encoded directly in the cron expression, e.g. `*/30 14-16 * * 1-5` for ECB)
- Backfill is incremental: fetches only from the last stored date forward

## Database

SQLite database with `rates`, `weekly_rates`, `monthly_rates`, `providers`, `currencies`, and `currency_coverages` tables.

### rates
- `date`, `base`, `quote`, `rate`, `provider`
- Unique index on `(provider, date, base, quote)`

### weekly_rates, monthly_rates
- Pre-aggregated rollups keyed by `bucket_date` (Monday for weekly, first-of-month for monthly)
- Rebuilt by `rake rollups:rebuild` and refreshed during backfill

### providers
- `key`, `name`, `rate_type`, `country_code`, `data_url`, `terms_url`, `publish_schedule`, `publish_cadence`, `coverage_start`, `pivot_currency`
- Seeded from `db/seeds/providers/*.json`
- `publish_schedule`: 5-field cron expression (minute hour day-of-month month day-of-week) in UTC, or `null` for historical-only providers. Convention: `*/30 H-H+2 * * D` where H is the publish hour and D is the day-of-week range, giving a 3-hour polling window.
- `publish_cadence`: one of `daily`, `weekly`, `monthly`, or `null` for historical-only providers. Dispatches `publishes_missed` to the right algorithm (per-fire-day count for daily; ISO-week bucket for weekly; year-month bucket for monthly).
- `coverage_start`: earliest date for historical data (used as backfill starting point)

### currencies
- `iso_code` (PK), `start_date`, `end_date`
- Global date range per currency, materialized during backfill

### currency_coverages
- `provider_key`, `iso_code`, `start_date`, `end_date`
- PK `(provider_key, iso_code)`
- Per-provider date range per currency, materialized during backfill

## Testing

```bash
APP_ENV=test bundle exec rake         # Run linter and test suite
APP_ENV=test bundle exec rake rubocop # Run linter only
APP_ENV=test bundle exec rake spec    # Run test suite only
```

Separate SQLite databases per environment (`APP_ENV`): test, development, production.

### Test stack

- Minitest
- Rack::Test for HTTP testing
- VCR + WebMock for HTTP recording/mocking
- Minitest-focus for targeted test runs
- Global transaction rollback via `Minitest::Spec#around`
- Test fixtures seed on suite load via `spec/helper.rb`

## Running Locally

```bash
bundle install                          # Install dependencies
bundle exec rake db:setup               # Run migrations and seed providers
bundle exec rake backfill               # Backfill all providers (takes a while)
bundle exec unicorn                     # Start server on port 8080
```

Or with Docker:
```bash
docker run -d -p 80:8080 lineofflight/frankfurter
```

### Legacy TLS

BCN's endpoint only supports TLS 1.0, which OpenSSL 3.5+ disables by default. Set `OPENSSL_CONF=config/openssl_legacy.cnf` to enable it. Without this, BCN skips backfill with "legacy TLS required, skipping".

## Rake Tasks

```bash
rake db:setup           # Run migrations and seed providers
rake db:migrate         # Run database migrations
rake db:seed            # Seed provider metadata
rake backfill           # Backfill all providers (threaded, incremental)
rake backfill[ecb]      # Backfill a single provider
rake rollups:rebuild    # Rebuild weekly and monthly rollups
rake rollups:rebuild[ecb] # Rebuild rollups for a single provider
```

## Adding a New Provider

See [.claude/skills/providers/SKILL.md](.claude/skills/providers/SKILL.md) for the full checklist and workflow.

## Historical Currencies

Pre-euro and pre-redenomination ISO 4217 codes are registered from
`db/seeds/historical_currencies.json` at boot via `Money::Currency.register`.
To add more historical codes, add entries to the seed file. When adding a new
provider, check whether it serves historical currencies and note them in coverage
research. To pick up previously-dropped records, re-backfill the provider from
its `coverage_start`:

```ruby
Provider["key"].backfill(after: Date.new(YYYY, 1, 1))
```

## Development Notes

- Ruby (see `Gemfile`)
- Linting: RuboCop with Shopify style guide (120-char line length)
- Migrations in `db/migrate/`
- Update `CHANGELOG.md` for changes that directly impact user experience

## Handling Data

Relay what providers publish. Don't editorialize.

## API Endpoints

### V2 (lib/versions/v2.rb)

Multi-provider API with blended rates. Full spec at `/v2/openapi.json`.

```
GET /v2/rates                                # latest blended rates
GET /v2/rates?base=USD                       # rebased
GET /v2/rates?quotes=USD,GBP                 # filtered
GET /v2/rates?date=2024-01-15                # specific date
GET /v2/rates?from=2024-01-01&to=2024-01-31  # date range
GET /v2/rates?providers=ecb,tcmb             # filter by providers
GET /v2/currencies                           # currencies with names and providers
GET /v2/providers                            # available data providers
```

Response: normalized array of `{ date, base, quote, rate }` records.

### V1 (lib/versions/v1.rb)

Frozen legacy API, ECB-only. Full spec at `/v1/openapi.json`.
