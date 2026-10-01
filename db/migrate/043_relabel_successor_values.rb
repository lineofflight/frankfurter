# frozen_string_literal: true

require "bucket"

# Sources that keep a retired code after a redenomination and quote the successor under it, found by comparing each
# provider's values past the retirement against other providers' successor rates. The adapters now emit the successor;
# this repairs what is stored, since insert-only backfill never rewrites a row:
#
#   CBG   SLL -> SLE from 2022-07-01
#   CBU   TRL -> TRY from 2005-01-04
#   LB    BYR -> BYB before 2000-01-01       the 1994 ruble under its successor's code
#   NBU   RUR -> RUB from 1998-01-01
#   NBU   BGL -> BGN from 1999-08-01         per 100 BGN from 2000, though the units field reads 1000
#   NBU   TRL -> TRY from 2005-01-06         per 100 of the successor to 2014, though the units field reads 10000
#   NBU   ROL -> RON from 2005-07-01         likewise
#   NBU   AZM -> AZN from 2006-01-06         likewise
#   NBU   TMM -> TMT from 2009-01-06         likewise
#   BNA   MZM -> MZN from 2006-07-01
#   BNA   STD -> STN from 2023-02-22
#   BNA   VEF -> VES from 2023-10-18         BNA's own VES row wins the one day it publishes both
#   BDI   ZWD -> ZWR from 2008-08-01, ZWL from 2009-02-03
#   NBP   ZWR -> ZWL from 2009-02-25
#   BAM   MRO -> MRU from 2018-01-03         53 rows quoted per 100 under a unit of 1
#   BOTA  ZMK -> ZMW from 2013-01-01
#   NBKR  BYR deleted from 2016-07-01        a frozen 2016 rate the live feed re-dates every week
#
# Rollups are rebuilt for every affected provider bucket and coverage is recomputed. The blend tables are left as they
# are: the repaired history spans 1994 to today, so refreshing it is a full rebuild, too slow for a migration that runs
# before the app starts. Clearing them instead, as 041 and 042 did, sends every request to live compute until the
# scheduler rebuilds them. Run `rake blend:rebuild` after deploy; it rebuilds in place and keeps the tables serving.
Sequel.migration do
  up do
    require "currency_summary"
    require "rate_precision"

    changes = [
      { provider: "CBG", from: "SLL", to: "SLE", since: "2022-07-01" },
      { provider: "CBU", from: "TRL", to: "TRY", since: "2005-01-04" },
      { provider: "LB", from: "BYR", to: "BYB", before: "2000-01-01" },
      { provider: "NBU", from: "RUR", to: "RUB", since: "1998-01-01" },
      { provider: "NBU", from: "BGL", to: "BGN", since: "1999-08-01", before: "2000-01-01" },
      { provider: "NBU", from: "BGL", to: "BGN", since: "2000-01-01", factor: 10 },
      { provider: "NBU", from: "TRL", to: "TRY", since: "2005-01-06", factor: 100 },
      { provider: "NBU", from: "ROL", to: "RON", since: "2005-07-01", factor: 100 },
      { provider: "NBU", from: "AZM", to: "AZN", since: "2006-01-06", factor: 100 },
      { provider: "NBU", from: "TMM", to: "TMT", since: "2009-01-06", factor: 100 },
      { provider: "BNA", from: "MZM", to: "MZN", since: "2006-07-01" },
      { provider: "BNA", from: "STD", to: "STN", since: "2023-02-22" },
      { provider: "BNA", from: "VEF", to: "VES", since: "2023-10-18", keep_existing: true },
      { provider: "BDI", from: "ZWD", to: "ZWR", since: "2008-08-01", before: "2009-02-03" },
      { provider: "BDI", from: "ZWD", to: "ZWL", since: "2009-02-03" },
      { provider: "NBP", from: "ZWR", to: "ZWL", since: "2009-02-25" },
      # The new ouguiya trades near 0.25 MAD, so a stored rate above 1 is a per-100 quote.
      { provider: "BAM", from: "MRO", to: "MRU", since: "2018-01-03", filter: Sequel[:rate] <= 1 },
      { provider: "BAM", from: "MRO", to: "MRU", since: "2018-01-03", filter: Sequel[:rate] > 1, factor: 0.01 },
      { provider: "BOTA", from: "ZMK", to: "ZMW", since: "2013-01-01" },
      { provider: "NBKR", from: "BYR", to: nil, since: "2016-07-01" },
    ]
    rollups = { weekly_rates: Bucket.week, monthly_rates: Bucket.month }
    either_side = ->(code) { Sequel.|({ base: code }, { quote: code }) }
    scope_for = lambda do |change|
      scope = from(:rates).where(provider: change[:provider]).where(either_side[change[:from]])
      scope = scope.where(Sequel[:date] >= change[:since]) if change[:since]
      scope = scope.where(Sequel[:date] < change[:before]) if change[:before]
      change[:filter] ? scope.where(change[:filter]) : scope
    end

    transaction do
      next if changes.all? { |change| scope_for[change].empty? }

      buckets = Hash.new { |hash, key| hash[key] = [] }
      codes = Hash.new { |hash, key| hash[key] = [] }
      changes.each do |change|
        provider = change[:provider]
        scope = scope_for[change]
        rollups.each { |table, bucket| buckets[[provider, table]] |= scope.select_map(bucket) }
        codes[provider] |= [change[:from], change[:to]].compact

        unless change[:to]
          scope.delete
          next
        end

        scope.all.each do |row|
          key = row.slice(:provider, :date, :base, :quote)
          target = key.transform_values { |value| value == change[:from] ? change[:to] : value }
          components = row.slice(:mid, :bid, :ask)
          if (factor = change[:factor])
            components = components.transform_values { |value| value && RatePrecision.normalize(value * factor) }
          end

          existing = from(:rates).where(target).first
          if existing
            # Equal duplicates collapse. A disagreement needs source evidence, not an arbitrary winner, unless the
            # source publishes the successor under its own code that day.
            unless change[:keep_existing] || existing.slice(:mid, :bid, :ask) == components
              raise "#{provider}: conflicting #{change[:from]}/#{change[:to]} components on #{row[:date]}"
            end

            from(:rates).where(key).delete
          else
            from(:rates).where(key).update(target.slice(:base, :quote).merge(components))
          end
        end
      end

      buckets.each do |(provider, table), dates|
        bucket = rollups.fetch(table)
        from(table).where(provider:, bucket_date: dates).delete
        from(table).insert(
          [:bucket_date, :provider, :base, :quote, :rate],
          from(:rates).where(provider:).where(bucket => dates)
            .select(bucket, :provider, :base, :quote, Sequel.function(:avg, :rate))
            .group(:provider, :base, :quote, bucket),
        )
      end
      codes.each { |provider, iso_codes| CurrencySummary.refresh(self, iso_codes, provider:) }
    end
  end

  down do
    # Irreversible: the adapters no longer emit the retired labels, and the rescaled values match the published units.
  end
end
