# frozen_string_literal: true

require "bucket"

# NBK labels the old Belarusian ruble BYN from the start of its series, so blended BYN before the 2016-07-01
# redenomination came out at the old ruble's scale, 2933.80 to the dollar on 2010-06-01. CBA swaps the two labels on
# four days around the switch. The adapters now emit the right code and unit; this repairs what is stored, since
# insert-only backfill never rewrites a row:
#
#   NBK   BYN -> BYR before 2016-07-04
#   CBA   BYN -> BYR before 2016-07-01
#   CBA   BYR -> BYN from 2016-07-01, per ruble, though the amount field reads 10
#
# NBK's values are already in old rubles (100 "BYN" = 1.68 KZT on 2016-07-01, 1 BYN = 170.73 KZT on 2016-07-04), and
# CBA's stray BYN row on 2016-01-08 repeats that day's BYR quote, so an equal duplicate collapses into it. CBA's three
# BYR rows from 2016-10-25 to 2016-10-27, stored only where CBA's BYR series has been backfilled, carry the new ruble's
# rate: 10 "BYR" = 249.91 AMD, between 1 BYN = 249.53 AMD on 2016-10-24 and 248.86 AMD on 2016-10-28.
#
# Rollups are rebuilt for every affected provider bucket, coverage is recomputed and the repaired dates are rescreened
# for spikes. The blend tables are left as they are. Run `rake blend:rebuild` after deploy; it rebuilds in place and
# keeps the tables serving.
Sequel.migration do
  up do
    require "currency_summary"
    require "rate_precision"
    require "rate_spike"

    changes = [
      { provider: "NBK", from: "BYN", to: "BYR", dates: ..."2016-07-04" },
      { provider: "CBA", from: "BYN", to: "BYR", dates: ..."2016-07-01" },
      { provider: "CBA", from: "BYR", to: "BYN", dates: "2016-07-01"..., factor: 10 },
    ]
    rollups = { weekly_rates: Bucket.week, monthly_rates: Bucket.month }
    scope_for = lambda do |change|
      from(:rates).where(provider: change[:provider], date: change[:dates])
        .where(Sequel.|({ base: change[:from] }, { quote: change[:from] }))
    end

    transaction do
      next if changes.all? { |change| scope_for[change].empty? }

      buckets = Hash.new { |hash, key| hash[key] = [] }
      codes = Hash.new { |hash, key| hash[key] = [] }
      dates = Hash.new { |hash, key| hash[key] = [] }
      changes.each do |change|
        provider = change[:provider]
        scope = scope_for[change]
        rollups.each { |table, bucket| buckets[[provider, table]] |= scope.select_map(bucket) }
        codes[provider] |= [change[:from], change[:to]]
        dates[provider] |= scope.select_map(:date)

        scope.all.each do |row|
          key = row.slice(:provider, :date, :base, :quote)
          target = key.transform_values { |value| value == change[:from] ? change[:to] : value }
          components = row.slice(:mid, :bid, :ask)
          if (factor = change[:factor])
            # The adapter divided by the amount field's 10, so a rate for one unit of the base grows tenfold, and a rate
            # against it shrinks.
            factor = 1.0 / factor if row[:quote] == change[:from]
            components = components.transform_values { |value| value && RatePrecision.normalize(value * factor) }
          end

          existing = from(:rates).where(target).first
          if existing
            # Equal duplicates collapse. A disagreement needs source evidence, not an arbitrary winner.
            unless existing.slice(:mid, :bid, :ask) == components
              raise "#{provider}: conflicting #{change[:from]}/#{change[:to]} components on #{row[:date]}"
            end

            from(:rates).where(key).delete
          else
            from(:rates).where(key).update(target.slice(:base, :quote).merge(components))
          end
        end
      end

      buckets.each do |(provider, table), bucket_dates|
        bucket = rollups.fetch(table)
        from(table).where(provider:, bucket_date: bucket_dates).delete
        from(table).insert(
          [:bucket_date, :provider, :base, :quote, :rate],
          from(:rates).where(provider:).where(bucket => bucket_dates)
            .select(bucket, :provider, :base, :quote, Sequel.function(:avg, :rate))
            .group(:provider, :base, :quote, bucket),
        )
      end
      codes.each { |provider, iso_codes| CurrencySummary.refresh(self, iso_codes, provider:) }
      dates.each { |provider, list| RateSpike.refresh(provider, list) }
    end
  end

  down do
    # Irreversible: the adapters no longer emit these labels, and the rescaled values match the quoted units.
  end
end
