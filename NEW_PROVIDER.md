# Adding a New Provider

Checklist for adding a new exchange rate data provider. Each step references an existing provider as a pattern to follow.

## Before You Start

- Identify the API endpoint and authentication requirements
- Confirm the base currency and available quote currencies
- Check the publish schedule (timezone, frequency, days of week)
- Determine the earliest available date for historical data

## Implementation Checklist

### 1. Provider class — `lib/providers/<key>.rb`

Inherit from `Providers::Base`. See any existing provider for the pattern (e.g. `lib/providers/boi.rb`).

Required:
- Class methods: `key` (uppercase string), `name` (display name), `earliest_date` (optional)
- `fetch(since: nil, upto: nil)` — fetches from the source API, populates `@dataset`
- `parse(data)` — separate method for unit-testable parsing logic
- Each record: `{ provider: key, date:, base:, quote:, rate: }`

Optional:
- Override `self.backfill(range: N)` if the API needs chunked requests (e.g. max 100 results per call)

Notes:
- The `base` and `quote` in each record are determined by the data, not a class method
- Handle unit multipliers (per-100, per-1000) by dividing to normalize to per-1-unit rates
- Rescue network errors (`Net::OpenTimeout`, `Net::ReadTimeout`) and return `self` with empty dataset

### 2. Tests — `spec/providers/<key>_spec.rb`

Follow the pattern in `spec/providers/boi_spec.rb` or `spec/providers/bccr_spec.rb`:

- VCR cassette setup in `before`/`after` blocks
- Integration test: `fetch(since:, upto:).import` then assert rates were stored
- Parse unit tests: call `parse` directly with inline fixture data
- Test edge cases: unit multipliers, empty values, invalid data

VCR cassettes (`spec/vcr_cassettes/<key>.yml`) are auto-created on the first live test run. Pin dates in tests — never use `Date.today` with VCR.

### 3. Rake task — `lib/tasks/<key>.rake`

```ruby
# frozen_string_literal: true

namespace :<key> do
  desc "Backfill <NAME> rates"
  task :backfill do
    require "providers/<key>"
    Providers::<CLASS>.backfill
  end
end
```

### 4. Wire into the backfill aggregate — `lib/tasks/import.rake`

Add `"<key>:backfill"` to the dependency list.

### 5. Wire into the scheduler — `bin/schedule`

- Add to the startup backfill array
- Add a cron entry aligned with the provider's publish window (check timezone!)
- Use `overlap: false` to prevent concurrent runs

### 6. Register in V2 API — `lib/versions/v2.rb`

Add `require "providers/<key>"` to the require block. The provider auto-registers via the `inherited` hook on `Providers::Base`.

### 7. Seed provider metadata — `db/seeds/providers.json`

Add an entry with: `key`, `name`, `description`, `data_url`, `terms_url` (nullable).

### 8. Verify

```bash
APP_ENV=test bundle exec rake spec                    # All tests pass
APP_ENV=test bundle exec rake rubocop                 # No lint issues
bundle exec rake db:seed                              # Provider appears in seed data
bundle exec rake <key>:backfill                       # Live backfill works
```
