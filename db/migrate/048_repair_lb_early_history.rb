# frozen_string_literal: true

require "bucket"

# LB's 1994 to 1998 bulletins carry rows the adapter now reads differently. This repairs what is stored, since
# insert-only backfill never rewrites a row:
#
#   LB    GER                     dropped, a copy of LB's Russian ruble quote for its whole run
#   LB    TJR before 1995-07-11   dropped, the same copy
#   LB    TMM before 1995-11-30   dropped, the same copy
#   LB    BYB before 1994-08-20   per 10 rubles, though the amount field reads 100
#   LB    YUN -> YUM              the post-1994 dinar under a code ISO retired in 1992
#
# The copies repeat LB's RUB quote to the digit: 100 "TMT" = 100 RUB = 0.0874 LTL on 1995-11-29, then 100 TMT = 0.2759
# LTL on 1995-11-30. The rubles are the nominal ones Belarus denominated tenfold on 1994-08-20: 100 "BYR" = 0.0140 LTL
# on 1994-08-19 and 0.1421 on 1994-08-22. GER, TJR and YUN are stored only where a full backfill ran after ingest
# stopped dropping unrecognised codes.
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
      { provider: "LB", from: "GER", dates: ..."1995-09-29" },
      { provider: "LB", from: "TJR", dates: ..."1995-07-11" },
      { provider: "LB", from: "TMM", dates: ..."1995-11-30" },
      { provider: "LB", from: "BYB", to: "BYB", dates: ..."1994-08-20", factor: 10 },
      { provider: "LB", from: "YUN", to: "YUM", dates: ..."1998-07-07" },
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
        codes[provider] |= [change[:from], change[:to]].compact
        dates[provider] |= scope.select_map(:date)

        # A copy prices the ruble, which LB's RUB row for the same day already carries.
        unless change[:to]
          scope.delete
          next
        end

        scope.all.each do |row|
          key = row.slice(:provider, :date, :base, :quote)
          target = key.transform_values { |value| value == change[:from] ? change[:to] : value }
          components = row.slice(:mid, :bid, :ask)
          if (factor = change[:factor])
            # The adapter divided by the amount field's 100 where 10 rubles were meant, so a rate for one unit of the
            # base grows tenfold, and a rate against it shrinks.
            factor = 1.0 / factor if row[:quote] == change[:from]
            components = components.transform_values { |value| value && RatePrecision.normalize(value * factor) }
          end

          existing = (from(:rates).where(target).first unless target == key)
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
    # Irreversible: the adapter no longer emits these rows or labels, and the rescaled values match the quoted unit.
  end
end
