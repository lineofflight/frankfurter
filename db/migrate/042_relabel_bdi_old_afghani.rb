# frozen_string_literal: true

require "bucket"

# Banca d'Italia labels its old-afghani quotes AFN. Until 2004-03-31 they hold the frozen official rate of 4750 AFA per
# dollar, long past the October 2002 redenomination, and on 2004-04-01 they switch to 47.5 new afghani. The adapter now
# emits AFA before that date; this relabels what is stored. The values are right and only the code is wrong, so rows are
# updated in place rather than refetched.
#
# BDI was the only daily AFN source before NBP joined on 2003-11-12, so the AFN blend ran at old-afghani magnitudes from
# 1999 and averaged them with NBP's new afghani until the switch. BDI's AFA and AFN rollups are rebuilt for the affected
# buckets and both codes' coverage is recomputed; AFA's defunct entry keeps the rows from 2002-10-07 out of blends and
# the catalogue. As in 041, the grouped blends for those buckets are dropped and the daily blend is cleared for the
# scheduler to rebuild.
Sequel.migration do
  up do
    require "blended_weekly_rate"
    require "blended_monthly_rate"
    require "currency_summary"

    codes = ["AFA", "AFN"]
    rollups = {
      weekly_rates: [Bucket.week, BlendedWeeklyRate],
      monthly_rates: [Bucket.month, BlendedMonthlyRate],
    }
    either_side = ->(code) { Sequel.|({ base: code }, { quote: code }) }

    transaction do
      scope = from(:rates).where(provider: "BDI").where(either_side["AFN"]).where(Sequel[:date] < "2004-04-01")
      next if scope.empty?

      buckets = rollups.transform_values { |bucket, _| scope.select_map(bucket).uniq }
      rows = scope.all
      rows.each do |row|
        key = row.slice(:provider, :date, :base, :quote)
        target = key.transform_values { |value| value == "AFN" ? "AFA" : value }
        existing = from(:rates).where(target).first
        if existing
          # Equal duplicates collapse. A disagreement needs source evidence, not an arbitrary winner.
          unless existing.slice(:mid, :bid, :ask) == row.slice(:mid, :bid, :ask)
            raise "BDI: conflicting AFN/AFA components on #{row[:date]}"
          end

          from(:rates).where(key).delete
        else
          from(:rates).where(key).update(target.slice(:base, :quote))
        end
      end

      rollups.each do |table, (bucket, blend)|
        dates = buckets.fetch(table)
        blend.dataset.where(bucket_date: dates).delete
        from(table).where(provider: "BDI", bucket_date: dates).where(either_side[codes]).delete
        from(table).insert(
          [:bucket_date, :provider, :base, :quote, :rate],
          from(:rates).where(provider: "BDI").where(either_side[codes]).where(bucket => dates)
            .select(bucket, :provider, :base, :quote, Sequel.function(:avg, :rate))
            .group(:provider, :base, :quote, bucket),
        )
      end
      CurrencySummary.refresh(self, codes, provider: "BDI")

      # Daily readiness checks only the earliest date. Partial invalidation could serve an incomplete table, so clear it
      # entirely. The scheduler rebuilds daily history and populates missing grouped buckets after startup.
      from(:blended_rates).delete
    end
  end

  down do
    # Irreversible: the adapter no longer emits AFN for BDI's old-afghani rows.
  end
end
