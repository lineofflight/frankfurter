# frozen_string_literal: true

require "bucket"

# LB labels five redenominated currencies' old values with the new code until a day or more after the switch, so each
# series stepped by 1000x or 10000x, and CBA's amount field understated two series tenfold. The adapters now emit the
# right code and unit; this repairs what is stored, since insert-only backfill never rewrites a row:
#
#   LB    PLN -> PLZ before 1995-01-03
#   LB    RUB -> RUR before 1998-01-05
#   LB    BGN -> BGL before 1999-07-07
#   LB    RON -> ROL before 2005-07-04
#   LB    MZN -> MZM before 2006-07-10
#   CBA   TJS -> TJR before 2000-11-01, per 100 rubles, though the amount field reads 10
#   CBA   KZT before 2005-01-04, per 10 tenge, though the amount field reads 1
#
# Each relabel keeps LB's values, which are already in the old unit (1000 "PLN" = 0.1641 LTL on 1995-01-02, when the old
# zloty traded near 24,500 to the dollar), and each rescale brings CBA in line with other sources on the same dates: its
# Tajik ruble came out at 10 to 12 times NBU's and CBR's TJR rates, and its tenge at 9 to 10 times the KZT median.
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
      { provider: "LB", from: "PLN", to: "PLZ", before: "1995-01-03" },
      { provider: "LB", from: "RUB", to: "RUR", before: "1998-01-05" },
      { provider: "LB", from: "BGN", to: "BGL", before: "1999-07-07" },
      { provider: "LB", from: "RON", to: "ROL", before: "2005-07-04" },
      { provider: "LB", from: "MZN", to: "MZM", before: "2006-07-10" },
      { provider: "CBA", from: "TJS", to: "TJR", before: "2000-11-01", factor: 0.1 },
      { provider: "CBA", from: "KZT", to: "KZT", before: "2005-01-04", factor: 0.1 },
    ]
    rollups = { weekly_rates: Bucket.week, monthly_rates: Bucket.month }
    scope_for = lambda do |change|
      from(:rates).where(provider: change[:provider])
        .where(Sequel.|({ base: change[:from] }, { quote: change[:from] }))
        .where(Sequel[:date] < change[:before])
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
            # The rate stands for one unit of the base, so a per-10 quote of the base shrinks, and a quote against it
            # grows.
            factor = 1 / factor if row[:quote] == change[:from]
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
      dates.each { |provider, list| RateSpike.refresh(provider, list) }
    end
  end

  down do
    # Irreversible: the adapters no longer emit these labels, and the rescaled values match the quoted units.
  end
end
