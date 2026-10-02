# Frankfurter

Frankfurter is a currency data API tracking reference exchange rates from institutional sources. Built with Roda, SQLite (via Sequel), Puma, Rufus scheduler, and Cloudflare.

## Commands

```bash
# Test and lint
APP_ENV=test bundle exec rake         # Lint and test suite
APP_ENV=test bundle exec rake rubocop # Linter only
APP_ENV=test bundle exec rake spec    # Test suite only (rerun once if SQLite3::BusyException occurs)

# Database & Blends
bundle exec rake db:setup             # Migrations + seed providers
bundle exec rake backfill             # Backfill all providers (or backfill[key])
bundle exec rake blend:rebuild        # Full rebuild of materialized blend tables
bundle exec rake blend:parity         # Verify table vs live blend query parity
bundle exec rake rollups:rebuild      # Rebuild weekly and monthly rollups

# Local server
bundle exec puma -C config/puma.rb    # Web server on :8080
bundle exec foreman start             # Web + scheduler (mirrors prod)
```

Separate SQLite databases per environment (`APP_ENV`). Minitest suite with VCR/WebMock and transactional rollback.

## Architecture & Blending

- **Pipeline:** Native provider rates, less one-day spikes (`RateSpike`), rebase to USD pivot (`BaseConversion`), outlier-screened (`Consensus`), recency-decay weighted (`WeightedAverage`), and peg-anchored (`PegAnchor`).
- **Materialized Blends:** Sparse tables (`blended_rates`, `blended_weekly_rates`, `blended_monthly_rates`) store precomputed USD blends. Plain V2 queries hit these tables directly; missing buckets or filtered queries fall back to live computation.
- **APIs:** Legacy V1 (ECB-only) and V2 (multi-provider/blended) mounted in `lib/app.rb`. OpenAPI specs at `lib/public/v1/openapi.json` and `lib/public/v2/openapi.json`.
- **Provider Ingestion:** Scheduled in `bin/schedule` via cron expressions in `db/seeds/providers/*.json`. Adapters (`lib/provider/adapters/`) handle pure fetch and parse. Use `midpoint(buy, sell)` for bid/ask sources to avoid float noise.
- **Currency Patches:** Historical currencies and name overrides are configured in `db/seeds/currency_patches.json` and loaded via `lib/currency_patches.rb`.

## Replacing Provider History

`rates` table inserts use `ON CONFLICT DO NOTHING`. Modifying historical rates requires deleting existing rows, refetching, and rebuilding downstream blends. Leave the blend tables in place: they keep serving the old values until the rebuild replaces them, whereas emptying them sends every request to live compute.

```ruby
provider = Provider["CBK"]
provider.adapter # Resolve before deleting
DB.transaction do
  [Rate, WeeklyRate, MonthlyRate].each { |model| model.where(provider: provider.key).delete }
end
provider.backfill(after: provider.coverage_start)

begin
  [BlendedRate, BlendedWeeklyRate, BlendedMonthlyRate].each(&:rebuild) if provider.blends?
ensure
  Cache.purge
end
```

Changes to blend rules, peg definitions, or provider eligibility require `rake blend:rebuild`.

## Bad Data

`rates` is the source's record; blends are ours. Provider queries return what the source published, mistakes included.

- **Reject at ingest** only what can't be a rate: non-positive values and dates past the future horizon (`RateValidation`).
- **Skip series that aren't exchange rates.** An interest rate or other non-FX figure a source files beside its rates (AMCM's `LIQ`) is skipped in the adapter and never stored. A foreign-exchange index a source publishes (NB's `TWI`, RBA's `FXRTWI`) stays in provider history, listed in `Provider::NON_CURRENCY_CODES` so it raises no unknown-currency alert.
- **Fix labels, not values.** When a source quotes a currency under the wrong code or unit (a retired code for its successor, a units field that disagrees with the quote), relabel or rescale in the adapter (`PREDECESSORS`, `SUCCESSORS`, `ALIASES`, unit overrides) with a spec, and add a data migration if the values are already imported. Confirm the unit against other providers on the same dates first.
- **Match the code to the unit of the number.** A series a source restated in today's unit (BIS, ECB's pre-2005 RON) keeps the new code even before the currency existed. Old-unit values under the new code move to the predecessor code up to that source's own switch date, which can trail the redenomination by days (`PREDECESSORS`, plus a data migration if already imported). An old code the app doesn't know needs a currency patch first.
- **Delete only rows the source didn't publish for that date**, such as a frozen rate a live feed re-dates every week, or history the source has since withdrawn.
- **Never delete a row because its value looks wrong.** Typos and spikes stay in `rates`. The blend screens them: `RateSpike` drops a one-day spike (3x off both neighbours, which agree) at any provider count, `Consensus` drops outliers when at least four providers quote a currency, and the defunct and nascent windows keep retired and not-yet-live codes out.
- **Data migrations leave blend tables alone.** Clearing them sends every request to live compute until the scheduler rebuilds. Run `rake blend:rebuild` after deploy instead; it rebuilds in place. A migration that relabels or deletes rows rescreens them with `RateSpike.refresh(provider, dates)`.

## Conventions

- **Data integrity:** Relay what providers publish. Don't editorialize. See [Bad Data](#bad-data).
- **Adding providers:** Follow [.agents/skills/implementing-providers/SKILL.md](.agents/skills/implementing-providers/SKILL.md).
- **Git commits:** Imperative mood, present tense, under 72 characters. Put issue references on the final line (`closes #123`).
- **Changelog (`CHANGELOG.md`):**
  - Audience: API consumers only. Omit internal plumbing (CI, refactors, scraping fixes).
  - Length: One line, a few words. Name what changed, never why or how.
  - Providers: `- <Full name> (<KEY>) as a data provider. (#n)`.
  - Sections: Keep a Changelog headers (`Added`, `Changed`, `Fixed`, `Deprecated`, `Removed`). Never duplicate a header.
  - Citations: Bare issue or PR number in parentheses: `(#123)`, never markdown links.
