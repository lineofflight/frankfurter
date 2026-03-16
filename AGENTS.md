# Frankfurter

Frankfurter is a free and open-source currency data API built with Ruby that tracks reference exchange rates from institutional sources like the European Central Bank and the Bank of Canada.

## Architecture

- Roda web framework with Rack middleware
- SQLite with Sequel ORM
- Unicorn
- Rufus scheduler

## Project Structure

```
lib/
├── app.rb                    # Main Roda application with routing
├── currency.rb               # Currency model
├── db.rb                     # Database configuration
├── providers/
│   ├── base.rb               # Provider interface and import logic
│   ├── ecb.rb                # European Central Bank provider
│   └── boc.rb                # Bank of Canada provider
├── versions/
│   ├── v1.rb                 # API v1 endpoints, query parsing, currency names
│   └── v1/
│       ├── roundable.rb      # Exchange rate rounding rules
│       └── quote/            # Rate quote classes (base, end_of_day, interval)
├── scheduler/daemon.rb       # Background data updates
└── tasks/
    ├── db.rake               # Database migrations and setup
    ├── ecb.rake              # ECB import/backfill/seed tasks
    └── boc.rake              # BOC import/backfill tasks

spec/                         # Minitest test suite
db/migrate/                   # Sequel migrations
db/seeds/                     # Offline seed data
```

## Key Components

### Providers (lib/providers/)
- `Providers::Base`: Shared interface — `key`, `base`, `current`, `historical`, `import`
- `Providers::ECB`: European Central Bank (EUR base, data since 1999)
- `Providers::BOC`: Bank of Canada (CAD base, data since 2017)
- Providers fetch and parse rates, `import` writes to DB via upsert
- Usage: `Providers::ECB.new.current.import`

### API (lib/app.rb, lib/versions/v1.rb)
- v1 API at `/v1/*` endpoints, scoped to ECB data
- CORS enabled for all origins
- JSON responses with 900-second caching

### Scheduler (lib/scheduler/daemon.rb, bin/schedule)
- Runs each provider's import task on its own cron schedule
- ECB: weekdays 15:00-17:00 UTC
- BOC: weekdays 20:00-22:00 UTC

## Database

SQLite database with single `currencies` table:
- `date`: DATE
- `base`: VARCHAR (source's native base currency)
- `quote`: VARCHAR (quoted currency code)
- `rate`: DECIMAL (exchange rate)
- `source`: VARCHAR (data provider identifier)

Unique index on `(source, date, quote)`.

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

## Running Locally

```bash
bundle install              # Install dependencies
bundle exec rake db:prepare # Run migrations and backfill all providers
bundle exec unicorn         # Start server on port 8080
```

Or with Docker:
```bash
docker run -d -p 80:8080 lineofflight/frankfurter
```

## Rake Tasks

```bash
rake ecb:import    # Import current ECB rates
rake ecb:backfill  # Import all historical ECB rates
rake ecb:seed      # Seed database from saved ECB data
rake boc:import    # Import current BOC rates
rake boc:backfill  # Import all historical BOC rates
rake db:prepare    # Run migrations and backfill all providers
```

## Development Notes

- Ruby (see `Gemfile`)
- Linting: RuboCop with Shopify style guide
- Migrations in `db/migrate/`

## API Endpoints

See `lib/versions/v1.rb` for current endpoint implementations:
- Latest rates
- Historical rates by date
- Date range queries
- Currency conversions
- Available currencies list

OpenAPI spec available at `/v1/openapi.json`.
