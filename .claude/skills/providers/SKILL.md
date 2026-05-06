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
- Determine the earliest available date for historical data (goes in `coverage_start` in the seed file)

## Implementation Checklist

### 1. Adapter class — `lib/provider/adapters/<key>.rb`

Inherit from `Provider::Adapters::Adapter`. See any existing adapter for the pattern (e.g. `lib/provider/adapters/boi.rb`).

Required:
- `fetch(after: nil, upto: nil)` — fetches from the source API, returns an array of records
- Each record: `{ date:, base:, quote:, rate: }` (no `provider:` — the Provider model stamps that during import)
- **Rate direction**: match the provider's native convention. `pivot_currency` may appear as either `base` or `quote` depending on the source — don't invert.
  - ECB publishes `1 EUR = X foreign`, pivot EUR goes in `base` (see `lib/provider/adapters/ecb.rb`).
  - NBG and BBK publish `1 foreign = X pivot`, pivot goes in `quote` (see `lib/provider/adapters/nbg.rb` and `lib/provider/adapters/bbk.rb`).
  - Store what the provider returns. Inverting in the adapter invites direction bugs and diverges from the blender's expectations.

Optional class methods (inside `class << self`):
- `backfill_range = N` — if the API needs chunked requests (e.g. max 100 results per call). The base class `fetch_each` uses this to iterate in windows.
- `def api_key = ENV["X_API_KEY"] || raise(Unavailable, "no API key")` — if the API requires authentication. This is not a blocker — implement the adapter regardless. It activates when the key is configured at deploy time.

Notes:
- Adapters have **no `key` or `name`** — Provider model owns identity. The adapter class name must match the provider key (e.g., `Provider::Adapters::ECB` for key `"ECB"`).
- The `base` and `quote` in each record are determined by the data, not a class method
- `parse` is a convention (not enforced by the base class) — most adapters define a `parse` method for unit-testable parsing, called from `fetch`
- Handle unit multipliers (per-100, per-1000) by dividing to normalize to per-1-unit rates. Guard against zero units before dividing.
- **Do not rescue errors** — let HTTP errors, timeouts, parse failures, and other exceptions bubble up. The scheduler handles retries; swallowing errors silently hides broken providers.
- **Per-day APIs**: Some APIs only return rates for a single date per request. A full backfill from e.g. 2000 means ~6,800 requests. Use `backfill_range` to chunk into small windows (e.g. 30 days) and add a `sleep` between requests to be polite. The base class `fetch_each` handles the iteration loop. See `lib/provider/adapters/nbg.rb` for a working example.

### 2. Tests — `spec/provider/adapters/<key>_spec.rb`

Follow the pattern in `spec/provider/adapters/boi_spec.rb` or `spec/provider/adapters/bccr_spec.rb`:

- VCR cassette setup in `before`/`after` blocks
- Integration test: `adapter.fetch(after:, upto:)`, assert dataset is non-empty and has expected structure
- Parse unit tests: call `parse` directly with inline fixture data
- Test edge cases: unit multipliers, empty values, invalid data

VCR cassettes (`spec/vcr_cassettes/<key>.yml`) are auto-created on the first live test run. Pin dates in tests — never use `Date.today` with VCR. **Never hand-craft or fabricate cassettes** — they must be recorded from a live API response. Use narrow date ranges in integration tests (3-5 days) to keep cassettes small and test runs fast.

**Avoiding time bombs**: Always pass explicit `upto:` dates in tests, even when the provider defaults to `Date.today`. If `upto` is omitted, the fetch will reach into unrecorded months and hit VCR errors on the 1st of the next month. Similarly, avoid assertions with hardcoded bounds on date counts (e.g. `<= 13` months) that break at month boundaries.

### 3. Seed provider metadata — `db/seeds/providers/<key>.json`

Create a single JSON file (not an array) with: `key`, `name`, `description`, `pivot_currency`, `data_url`, `terms_url` (nullable), `publish_schedule` (5-field cron expression in UTC, e.g. `"*/30 14-16 * * 1-5"` for daily Mon-Fri with a 3-hour polling window starting at 14:00 UTC; `null` for providers without a recurring cadence), `publish_cadence` (one of `"daily"`, `"weekly"`, `"monthly"`, or `null` for historical-only providers; dispatches `publishes_missed` to the right algorithm — per-fire-day count for daily, ISO-week bucket for weekly, year-month bucket for monthly), `coverage_start` (earliest date for historical data, or null if unknown). Each provider has its own file — no shared file to conflict on.

The adapter class is auto-discovered from `lib/provider/adapters/` — no need to edit any wiring files.

### 4. Verify

```bash
APP_ENV=test bundle exec rake spec                    # All tests pass
APP_ENV=test bundle exec rake rubocop                 # No lint issues
bundle exec rake db:seed                              # Provider appears in seed data
bundle exec rake backfill[<key>]                      # Live backfill works
```

**Dry-run the backfill before shipping.** VCR tests only cover narrow date ranges. A real backfill exercises chunked iteration, API rate limits, and date range constraints that specs won't catch. Test at least one full `backfill_range` chunk against the live API to confirm the adapter works end-to-end — especially to verify the API's maximum allowed date range matches your `backfill_range` setting.

### 5. Sanity-check rates (before deploy)

After local backfill, compare the new provider's rates against an independent source **before pushing or deploying**. This catches direction bugs (base/quote swapped), unit errors (per-100 not normalized), or stale data before they reach production.

**Quick check — cross-reference with ECB rates in the local DB:**

```ruby
# In a console or one-liner: compare a sample of the new provider's rates against ECB
new_rates = Rate.where(provider: "<KEY>").where(date: Date.today - 7..Date.today).all
ecb_rates = Rate.where(provider: "ECB").where(date: Date.today - 7..Date.today).all
# Rebase both to EUR and compare overlapping quotes
```

**External check — use the `wise-api` skill** to compare against Wise mid-market rates. Sample a few major currency pairs (EUR/USD, EUR/GBP, EUR/JPY) and check deviation:

| Deviation | Assessment |
|-----------|-----------|
| < 0.5% | Good — normal institutional vs real-time spread |
| 0.5-1% | Acceptable for less-liquid pairs |
| > 1% | Investigate — possible direction or unit error |
| > 5% | Almost certainly a bug (e.g. base/quote inverted) |

**What to look for:**
- Rates that are the reciprocal of expected (base/quote swapped) — this was the HNB bug — see 'Rate direction' principle above
- Rates that are 10x or 100x off (unit multiplier not normalized)
- Rates that match another provider exactly but on wrong dates (date parsing bug)

## Extending an existing adapter

When you widen an existing adapter to emit new record shapes (a new currency, a new pair, a new report block), `Provider#backfill` resumes from `last_synced` — so already-synced environments only fetch the new shape from the current date forward. To populate history, hand-backfill once at deploy:

```ruby
Provider["KEY"].backfill(after: Date.new(YYYY, M, D))
```

A fresh DB doesn't need this — it starts from `coverage_start`.
