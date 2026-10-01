# frozen_string_literal: true

require "date"

require "bucket"

# A backfill that retained older history surfaced labels provider health could not place (#735-#738). The adapters now
# map them; this repairs what is stored, since insert-only backfill never rewrites a row and the corrected rows have new
# keys:
#
#   CBU   SDR -> XDR                   the pre-2008 label for the same unit
#   BOTA  MXM -> MZM                   old metical, July 1999
#   NBP   AON -> AOA, BYB -> BYR       retired labels on current values in 2002-2003 tables
#   NBP   AFA -> AFN from 2003-01-07   new-afghani values under the old label
#   BOI   BEL -> BEF per unit          published per 10 francs; ATS per 10, ESP per 100 and ITL per 1000 likewise
#   BOI   CBK_L deleted                the currency basket, not a currency
#
# Rollups are rebuilt for every affected provider bucket and the grouped blends for those buckets are dropped. The same
# backfill brought legacy series quoted years past their redenominations at fixed multiples of the successors (BDI's
# BGL, MZM and VEB run to 2004, 2006 and 2012). New defunct entries keep those observations out of blends and the
# catalogue, so their coverage is recomputed and grouped blends past each cutoff are dropped too. The daily blend is
# cleared for the scheduler to rebuild, and populate refills the grouped buckets.
Sequel.migration do
  up do
    require "blended_weekly_rate"
    require "blended_monthly_rate"
    require "currency_summary"
    require "rate_precision"

    relabels = [
      { provider: "CBU", from: "SDR", to: "XDR" },
      { provider: "BOTA", from: "MXM", to: "MZM" },
      { provider: "NBP", from: "AON", to: "AOA" },
      { provider: "NBP", from: "BYB", to: "BYR" },
      { provider: "NBP", from: "AFA", to: "AFN", since: "2003-01-07" },
      { provider: "BOI", from: "BEL", to: "BEF", unit: 10 },
      { provider: "BOI", from: "ATS", to: "ATS", unit: 10 },
      { provider: "BOI", from: "ESP", to: "ESP", unit: 100 },
      { provider: "BOI", from: "ITL", to: "ITL", unit: 1000 },
      { provider: "BOI", from: "CBK_L", to: nil },
    ]
    retired = {
      "ADP" => "2002-03-01", "AFA" => "2002-10-07", "BGL" => "1999-07-05", "BYB" => "2000-01-01",
      "MGF" => "2005-01-01", "MZM" => "2006-07-01", "SDD" => "2007-01-10", "SRG" => "2004-01-01",
      "VEB" => "2008-01-01",
    }
    rollups = {
      weekly_rates: [Bucket.week, :week, BlendedWeeklyRate],
      monthly_rates: [Bucket.month, :month, BlendedMonthlyRate],
    }
    either_side = ->(code) { Sequel.|({ base: code }, { quote: code }) }

    transaction do
      relabels.each do |change|
        scope = from(:rates).where(provider: change[:provider]).where(either_side[change[:from]])
        change[:scope] = change[:since] ? scope.where(Sequel[:date] >= change[:since]) : scope
      end
      leftovers = relabels.any? do |change|
        !from(:currency_exclusions).where(provider_key: change[:provider], iso_code: change[:from]).empty? ||
          rollups.keys.any? do |table|
            !from(table).where(provider: change[:provider]).where(either_side[change[:from]]).empty?
          end
      end
      expired = retired.to_h do |code, terminal|
        coverages = from(:currency_coverages).where(iso_code: code).where(Sequel[:end_date] >= terminal)
        [code, coverages.select_map(:provider_key)]
      end
      next if relabels.all? { |change| change[:scope].empty? } && !leftovers && expired.values.all?(&:empty?)

      buckets = Hash.new { |hash, key| hash[key] = [] }
      codes = Hash.new { |hash, key| hash[key] = [] }
      relabels.each do |change|
        provider = change[:provider]
        rollups.each do |table, (bucket, *)|
          stored = from(table).where(provider:).where(either_side[change[:from]])
          buckets[[provider, table]] |= change[:scope].select_map(bucket) | stored.select_map(:bucket_date)
        end
        rows = change[:scope].all
        codes[provider] |= [change[:from], change[:to]].compact | rows.flat_map { |row| row.values_at(:base, :quote) }

        unless change[:to]
          change[:scope].delete
          next
        end

        rows.each do |row|
          key = row.slice(:provider, :date, :base, :quote)
          target = key.transform_values { |value| value == change[:from] ? change[:to] : value }
          components = row.slice(:mid, :bid, :ask)
          if change[:unit]
            components = components.transform_values { |value| value && RatePrecision.normalize(value / change[:unit]) }
          end

          existing = from(:rates).where(target).first unless target == key
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
        next if dates.empty?

        bucket, _, blend = rollups.fetch(table)
        blend.dataset.where(bucket_date: dates).delete
        from(table).where(provider:, bucket_date: dates).delete
        from(table).insert(
          [:bucket_date, :provider, :base, :quote, :rate],
          from(:rates).where(provider:).where(bucket => dates)
            .select(bucket, :provider, :base, :quote, Sequel.function(:avg, :rate))
            .group(:provider, :base, :quote, bucket),
        )
      end
      codes.each { |provider, iso_codes| CurrencySummary.refresh(self, iso_codes, provider:) }

      expired.each do |code, providers|
        terminal = Date.parse(retired.fetch(code))
        providers.each do |provider|
          rollups.each do |table, (_, precision, blend)|
            # The bucket holding the last valid day straddles the cutoff, so it changes too.
            first = get(Bucket.expression(precision, (terminal - 1).to_s))
            stale = from(table).where(provider:).where(either_side[code]).where(Sequel[:bucket_date] >= first)
            blend.dataset.where(bucket_date: stale.select(:bucket_date)).delete
          end
          CurrencySummary.refresh(self, [code], provider:)
        end
      end

      # Daily readiness checks only the earliest date. Partial invalidation could serve an incomplete table, so clear it
      # entirely. The scheduler rebuilds daily history and populates missing grouped buckets after startup.
      from(:blended_rates).delete
    end
  end

  down do
    # Irreversible: the adapters no longer emit the retired labels, and the rescaled values match the published units.
  end
end
