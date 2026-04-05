---
description: Use when adding a new exchange rate data provider, implementing a provider from a GitHub issue, when the user mentions a new central bank or data source, or when working on any issue labeled "provider". Also use when asked to backfill, fix, or update an existing provider.
---

# Adding a New Provider

Checklist for adding a new exchange rate data provider. Each step references an existing provider as a pattern to follow.

## Before You Start

- Identify the API endpoint and authentication requirements
- **Verify the API is accessible** — make a test request and confirm you get a 200 response with valid data. If the API returns 403, times out, or is otherwise inaccessible, **stop here**. Do not proceed with a hand-crafted cassette or fake data.
- **Read the API docs** — understand pagination, date filtering, and rate limiting. Some APIs require specific params for date ranges (e.g., HKMA needs `choose=end_of_day` for `from`/`to` to work). Getting this right avoids downloading the entire dataset on every request.
- Confirm the base currency and available quote currencies
- Check the publish schedule (timezone, frequency, days of week)
- Determine the earliest available date for historical data

## Implementation Checklist

### 1. Adapter class — `lib/provider/adapters/<key>.rb`

Inherit from `Provider::Adapters::Adapter`. See any existing adapter for the pattern (e.g. `lib/provider/adapters/boi.rb`).

Required:
- `fetch(after: nil, upto: nil)` — fetches from the source API, returns an array of records
- `parse(data)` — separate method for unit-testable parsing logic
- Each record: `{ date:, base:, quote:, rate: }` (no `provider:` — the Provider model stamps that during import)

Optional class methods (inside `class << self`):
- `earliest_date` — earliest date for historical data
- `backfill_range = N` — if the API needs chunked requests (e.g. max 100 results per call)
- `api_key? = true` / `api_key = ENV["X_API_KEY"]` — if the API requires authentication

Notes:
- Adapters have **no `key` or `name`** — Provider model owns identity. The adapter class name must match the provider key (e.g., `Provider::Adapters::ECB` for key `"ECB"`).
- The `base` and `quote` in each record are determined by the data, not a class method
- Handle unit multipliers (per-100, per-1000) by dividing to normalize to per-1-unit rates. Guard against zero units before dividing.
- **Do not rescue errors** — let HTTP errors, timeouts, parse failures, and other exceptions bubble up. The scheduler handles retries; swallowing errors silently hides broken providers.
- **Per-day APIs**: Some APIs only return rates for a single date per request. A full backfill from e.g. 2000 means ~6,800 requests. Use `backfill_range` to chunk into small windows (e.g. 30 days) and add a `sleep` between requests to be polite. See `lib/provider/adapters/nbg.rb` for a working example.

### 2. Tests — `spec/provider/adapters/<key>_spec.rb`

Follow the pattern in `spec/provider/adapters/boi_spec.rb` or `spec/provider/adapters/bccr_spec.rb`:

- VCR cassette setup in `before`/`after` blocks
- Integration test: `adapter.fetch(after:, upto:)`, assert dataset is non-empty and has expected structure
- Parse unit tests: call `parse` directly with inline fixture data
- Test edge cases: unit multipliers, empty values, invalid data

VCR cassettes (`spec/vcr_cassettes/<key>.yml`) are auto-created on the first live test run. Pin dates in tests — never use `Date.today` with VCR. **Never hand-craft or fabricate cassettes** — they must be recorded from a live API response. Use narrow date ranges in integration tests (3-5 days) to keep cassettes small and test runs fast.

**Avoiding time bombs**: Always pass explicit `upto:` dates in tests, even when the provider defaults to `Date.today`. If `upto` is omitted, the fetch will reach into unrecorded months and hit VCR errors on the 1st of the next month. Similarly, avoid assertions with hardcoded bounds on date counts (e.g. `<= 13` months) that break at month boundaries.

### 3. Seed provider metadata — `db/seeds/providers.json`

Add an entry with: `key`, `name`, `description`, `data_url`, `terms_url` (nullable), `publish_time` (UTC hour), `publish_days` (cron-style day range, e.g. "1-5" for Mon-Fri).

The adapter class is auto-discovered from `lib/provider/adapters/` — no need to edit any wiring files.

### 4. Verify

```bash
APP_ENV=test bundle exec rake spec                    # All tests pass
APP_ENV=test bundle exec rake rubocop                 # No lint issues
bundle exec rake db:seed                              # Provider appears in seed data
bundle exec rake backfill[<key>]                      # Live backfill works
```
