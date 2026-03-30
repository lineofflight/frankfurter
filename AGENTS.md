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
├── cache.rb                  # Cloudflare cache purge
├── currency.rb               # Currency virtual model (UNION over rates)
├── provider.rb               # Provider model (Sequel, static cache)
├── rate.rb                   # Rate model with query scopes
├── db.rb                     # Database configuration
├── providers.rb              # Auto-requires all providers from providers/
├── providers/
│   ├── base.rb               # Provider interface: fetch, import, backfill
│   └── <key>.rb              # One file per provider (auto-discovered)
├── versions/
│   ├── v1.rb                 # Legacy API (ECB-only, frozen)
│   ├── v1/                   # V1 internals (quotes, rounding, currency names)
│   ├── v2.rb                 # Multi-provider API
│   └── v2/
│       └── query.rb          # V2 query builder (blending, filtering)
├── public/
│   ├── v1/openapi.json       # V1 OpenAPI spec
│   └── v2/openapi.json       # V2 OpenAPI spec
└── tasks/
    ├── db.rake               # Database migrations and setup
    ├── import.rake            # Aggregate backfill task
    └── <key>.rake             # One rake file per provider

spec/                         # Minitest test suite
db/migrate/                   # Sequel migrations
db/seeds/
    └── providers.json        # Provider metadata (key, name, description, urls)
```

## Key Components

### Providers (lib/providers/)
- `Providers::Base`: Shared interface — `key`, `base`, `fetch`, `import`, `backfill`
- `key`, `name` are class methods; instance methods delegate
- `fetch(since: nil, upto: nil)`: fetches rate data from the source API
- `self.backfill(range:)`: queries DB for last stored date, fetches forward, imports. `range:` enables chunked requests for APIs with result limits.
- `import`: writes to DB via upsert, filters excluded quotes (precious metals, SDR), purges Cloudflare cache
- All providers auto-register via `inherited` hook into `Providers.all`

### Models
- `Rate`: Sequel model on `rates` table. Scopes: `latest(date)`, `between(interval)`, `only(*quotes)`, `downsample(precision)`
- `Currency`: Virtual model backed by UNION query over rates. Derives currencies, date ranges from data.
- `Provider`: Sequel model on `providers` table (seeded from `db/seeds/providers.json`). Static cache.

### API (lib/app.rb)
- V1 at `/v1/*` — frozen legacy, ECB-only
- V2 at `/v2/*` — multi-provider with blended rates
- CORS enabled for all origins
- OpenAPI specs served as static files at `/v1/openapi.json` and `/v2/openapi.json`

### Scheduler (bin/schedule)
- Staggers startup backfill for all providers (2s apart)
- Cron schedules derived from `publish_time` and `publish_days` in the providers table
- Convention: poll every 30 min for 3 hours starting at `publish_time`
- Backfill is incremental: fetches only from the last stored date forward

## Database

SQLite database with `rates` and `providers` tables.

### rates
- `date`, `base`, `quote`, `rate`, `provider`
- Unique index on `(provider, date, base, quote)`

### providers
- `key`, `name`, `description`, `data_url`, `terms_url`, `publish_time`, `publish_days`
- Seeded from `db/seeds/providers.json`
- `publish_time`: UTC hour when the provider typically publishes new rates
- `publish_days`: cron-style day range (e.g. "1-5" for Mon-Fri, "0-4" for Sun-Thu)

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
bundle exec rake db:migrate db:seed     # Run migrations and seed providers
bundle exec rake backfill               # Backfill all providers (takes a while)
bundle exec unicorn                     # Start server on port 8080
```

Or with Docker:
```bash
docker run -d -p 80:8080 lineofflight/frankfurter
```

## Rake Tasks

```bash
rake db:migrate      # Run database migrations
rake db:seed         # Seed provider metadata
rake backfill        # Backfill all providers (incremental)
rake <key>:backfill  # Backfill a single provider (e.g. rake ecb:backfill)
```

## Adding a New Provider

See [NEW_PROVIDER.md](NEW_PROVIDER.md) for the full checklist and workflow.

## Development Notes

- Ruby (see `Gemfile`)
- Linting: RuboCop with Shopify style guide (120-char line length)
- Migrations in `db/migrate/`
- Update `CHANGELOG.md` for changes that directly impact user experience

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
