# frozen_string_literal: true

require "bucket"

# The history backfills stored four labels no currency list carries. The adapters now read them for what they price;
# this repairs what is stored, since insert-only backfill never rewrites a row:
#
#   AMCM  LIQ          dropped, an interest rate in percent that the adapter read as a price in patacas
#   CNB   BEC -> BEF   the convertible Belgian franc, to 1991-01-24
#   CNB   YUD -> YUN   the convertible dinar under the hard dinar's code, January 1991
#   NBU   ZAL -> TJS   the somoni under the financial rand's code, 2000-11-01 to 2002-11-01
#
# BEC matches CNB's LUF to the digit and BEF starts the day it stops. CNB's dinar stands at 9 to the mark, the
# convertible dinar's level. NBU's ZAL fills the gap between its Tajik ruble and somoni rows and runs into the latter: 1
# "ZAL" = 1.8052 UAH on 2002-11-01, 100 TJS = 180.5263 on 2002-12-01. None of the targets holds a row on these dates.
#
# CNB's XCU, the clearing ECU of Czech-Slovak payments, keeps its rows: it is registered as a currency, and seeding
# moves it from the unknown codes to the catalogue.
#
# Rollups are rebuilt for every affected provider bucket, coverage is recomputed and the repaired dates are rescreened
# for spikes. The blend tables are left as they are. Run `rake blend:rebuild` after deploy; it rebuilds in place and
# keeps the tables serving.
Sequel.migration do
  up do
    require "currency_summary"
    require "rate_spike"

    changes = [
      { provider: "AMCM", from: "LIQ" },
      { provider: "CNB", from: "BEC", to: "BEF" },
      { provider: "CNB", from: "YUD", to: "YUN" },
      { provider: "NBU", from: "ZAL", to: "TJS" },
    ]
    rollups = { weekly_rates: Bucket.week, monthly_rates: Bucket.month }
    scope_for = lambda do |change|
      from(:rates).where(provider: change[:provider])
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
        codes[provider] |= [change[:from], change[:to]].compact
        dates[provider] |= scope.select_map(:date)

        # Not an exchange rate, so there is no code to move it to.
        unless change[:to]
          scope.delete
          next
        end

        scope.all.each do |row|
          key = row.slice(:provider, :date, :base, :quote)
          target = key.transform_values { |value| value == change[:from] ? change[:to] : value }

          existing = from(:rates).where(target).first
          if existing
            # Equal duplicates collapse. A disagreement needs source evidence, not an arbitrary winner.
            unless existing.slice(:mid, :bid, :ask) == row.slice(:mid, :bid, :ask)
              raise "#{provider}: conflicting #{change[:from]}/#{change[:to]} components on #{row[:date]}"
            end

            from(:rates).where(key).delete
          else
            from(:rates).where(key).update(target.slice(:base, :quote))
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
    # Irreversible: the adapters no longer emit these labels.
  end
end
