# frozen_string_literal: true

require "bucket"

Sequel.migration do
  up do
    require "blended_rate"
    require "blended_weekly_rate"
    require "blended_monthly_rate"
    require "currency_summary"

    transaction do
      legacy = from(:rates).where(provider: "RBA").where(Sequel.|({ base: "SDR" }, { quote: "SDR" }))
      old_code = Sequel.|({ base: "SDR" }, { quote: "SDR" })
      old_rollups = [:weekly_rates, :monthly_rates].any? do |table|
        !from(table).where(provider: "RBA").where(old_code).empty?
      end
      excluded = !from(:currency_exclusions).where(provider_key: "RBA", iso_code: "SDR").empty?
      next if legacy.empty? && !old_rollups && !excluded

      codes = ["SDR", "XDR"]
      pairs = Sequel.|({ base: codes }, { quote: codes })
      rows = legacy.all
      counterparts = rows.flat_map { |row| row.values_at(:base, :quote) }

      rows.each do |row|
        key = row.slice(:provider, :date, :base, :quote)
        normalized = key.transform_values { |value| value == "SDR" ? "XDR" : value }
        existing = from(:rates).where(normalized).first
        if existing
          # Equal duplicates are safe to collapse. A disagreement needs source evidence, not an arbitrary winner.
          components = [:mid, :bid, :ask]
          unless existing.values_at(*components) == row.values_at(*components)
            raise "RBA: conflicting SDR/XDR components on #{row[:date]} (#{row[:base]}/#{row[:quote]})"
          end

          from(:rates).where(key).delete
        else
          from(:rates).where(key).update(normalized.slice(:base, :quote))
        end
      end

      {
        weekly_rates: [Bucket.week, BlendedWeeklyRate],
        monthly_rates: [Bucket.month, BlendedMonthlyRate],
      }.each do |table, (bucket, blend)|
        source = from(:rates).where(provider: "RBA").where(pairs)
        rollups = from(table).where(provider: "RBA").where(pairs)
        # Include old buckets that disappear, and replace complete blended buckets since cross rates also change.
        buckets = rollups.select_map(:bucket_date) | source.select_map(bucket)
        next if buckets.empty?

        blend.dataset.where(bucket_date: buckets).delete
        rollups.delete
        from(table).insert(
          [:bucket_date, :provider, :base, :quote, :rate],
          source.select(bucket, :provider, :base, :quote, Sequel.function(:avg, :rate))
            .group(:provider, :base, :quote, bucket),
        )
      end

      CurrencySummary.refresh(self, codes | counterparts | ["AUD"], provider: "RBA")
      # Daily readiness checks only the earliest date. Partial invalidation could serve an incomplete table, so clear it
      # entirely. The scheduler rebuilds daily history and populates missing grouped buckets after startup.
      from(:blended_rates).delete
    end
  end

  down do
    # Irreversible: SDR and XDR represent the same unit, and the adapter now emits only XDR.
  end
end
