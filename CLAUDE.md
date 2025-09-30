# Frankfurter

Frankfurter is a free and open-source currency data API built with Ruby that tracks reference exchange rates from institutional sources like the European Central Bank.

## Architecture

- Roda web framework with Rack middleware
- SQLite with Sequel ORM
- Unicorn
- Rufus scheduler

## Project Structure

```
lib/
├── app.rb              # Main Roda application with routing
├── bank.rb             # Data fetching and normalization
├── bank/feed.rb        # ECB feed parsing
├── currency.rb         # Currency model
├── db.rb               # Database configuration
├── query.rb            # Query parameter parsing
├── quote.rb            # Exchange rate calculations
├── quote/
│   ├── base.rb         # Base quote class
│   ├── end_of_day.rb   # Daily rate queries
│   └── interval.rb     # Date range queries
├── versions/v1.rb      # API v1 endpoints
└── scheduler/daemon.rb # Background data updates

spec/                   # Minitest test suite
db/migrate/            # Sequel migrations
```

## Key Components

### API (lib/app.rb, lib/versions/v1.rb)
- Versioned API at `/v1/*` endpoints
- CORS enabled for all origins
- Static file serving for root.json, OpenAPI spec, etc.
- JSON responses with 900-second caching

### Data Management (lib/bank.rb, lib/bank/feed.rb)
- Fetches rates from ECB XML feeds
- Normalizes data: `{ date, iso_code, rate }`
- Methods: `fetch_all!`, `fetch_current!`, `fetch_ninety_days!`
- Uses upserts (`insert_conflict`) for idempotent updates

### Currency Queries (lib/quote/)
- `Quote::EndOfDay`: Single-day exchange rates
- `Quote::Interval`: Multi-day date ranges
- Base currency conversion and rounding
- Filters by currency codes

### Scheduler (lib/scheduler/daemon.rb)
- Background job for periodic data fetching
- Runs `Bank.fetch_current!` on schedule

## Database

SQLite database with single `currencies` table:
- `date`: DATE
- `iso_code`: VARCHAR (currency code)
- `rate`: DECIMAL (exchange rate vs EUR)

Base currency is EUR from ECB data.

## Testing

Tests are in `spec/` mirroring `lib/` structure.

```bash
bundle exec rake rubocop # Run linter
bundle exec rake test    # Run test suite
```

### Test stack

- Minitest
- Rack::Test for HTTP testing
- VCR + WebMock for HTTP recording/mocking
- Minitest-focus for targeted test runs

## Running Locally

```bash
bundle install             # Install dependencies
bundle exec rake db:migrate # Run migrations
bundle exec rake db:seed   # Seed with historical data
bundle exec unicorn        # Start server on port 8080
```

Or with Docker:
```bash
docker run -d -p 80:8080 lineofflight/frankfurter
```

## Development Notes

- Ruby 3.4.6 required (see `Gemfile`)
- Linting: RuboCop with Shopify style guide
- Dependencies managed via `Gemfile.lock`
- Migrations in `db/migrate/`

## API Endpoints

See `lib/versions/v1.rb` for current endpoint implementations:
- Latest rates
- Historical rates by date
- Date range queries
- Currency conversions
- Available currencies list

OpenAPI spec available at `/v1/openapi.json`.
