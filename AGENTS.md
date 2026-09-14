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

- **Pipeline:** Native provider rates rebase to USD pivot (`BaseConversion`), outlier-screened (`Consensus`), recency-decay weighted (`WeightedAverage`), and peg-anchored (`PegAnchor`).
- **Materialized Blends:** Sparse tables (`blended_rates`, `blended_weekly_rates`, `blended_monthly_rates`) store precomputed USD blends. Plain V2 queries hit these tables directly; missing buckets or filtered queries fall back to live computation.
- **APIs:** Legacy V1 (ECB-only) and V2 (multi-provider/blended) mounted in `lib/app.rb`. OpenAPI specs at `lib/public/v1/openapi.json` and `lib/public/v2/openapi.json`.
- **Provider Ingestion:** Scheduled in `bin/schedule` via cron expressions in `db/seeds/providers/*.json`. Adapters (`lib/provider/adapters/`) handle pure fetch and parse. Use `midpoint(buy, sell)` for bid/ask sources to avoid float noise.
- **Currency Patches:** Historical currencies and name overrides are configured in `db/seeds/currency_patches.json` and loaded via `lib/currency_patches.rb`.

## Replacing Provider History

`rates` table inserts use `ON CONFLICT DO NOTHING`. Modifying historical rates requires deleting existing rows and explicitly invalidating downstream blends in the same transaction:

```ruby
provider = Provider["CBK"]
provider.adapter # Resolve before deleting
DB.transaction do
  if provider.blends?
    [BlendedWeeklyRate, BlendedMonthlyRate].each do |model|
      old_dates = model.source.where(provider: provider.key).select(:bucket_date)
      model.dataset.where(bucket_date: old_dates).delete
    end
    BlendedRate.dataset.delete
  end
  [Rate, WeeklyRate, MonthlyRate].each { |model| model.where(provider: provider.key).delete }
end
Cache.purge
provider.backfill(after: provider.coverage_start)

begin
  [BlendedWeeklyRate, BlendedMonthlyRate].each(&:populate)
  BlendedRate.rebuild if provider.blends?
ensure
  Cache.purge
end
```

Changes to blend rules, peg definitions, or provider eligibility require `rake blend:rebuild`.

## Conventions

- **Data integrity:** Relay what providers publish. Don't editorialize.
- **Adding providers:** Follow [.agents/skills/implementing-providers/SKILL.md](.agents/skills/implementing-providers/SKILL.md).
- **Git commits:** Imperative mood, present tense, under 72 characters. Put issue references on the final line (`closes #123`).
- **Changelog (`CHANGELOG.md`):**
  - Audience: API consumers only. Omit internal plumbing (CI, refactors, scraping fixes).
  - Length: One line, a few words. Name what changed, never why or how.
  - Providers: `- <Full name> (<KEY>) as a data provider. (#n)`.
  - Sections: Keep a Changelog headers (`Added`, `Changed`, `Fixed`, `Deprecated`, `Removed`). Never duplicate a header.
  - Citations: Bare issue or PR number in parentheses: `(#123)`, never markdown links.
