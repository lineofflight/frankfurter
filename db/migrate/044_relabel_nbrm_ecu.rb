# frozen_string_literal: true

require "bucket"

# NBRM codes the ECU as XBA, the bond-market European Composite Unit, and keeps the label to May 1999, quoting the same
# value it publishes under EUR. The adapter now emits XEU before the euro and EUR after; this repairs what is stored,
# since insert-only backfill never rewrites a row:
#
#   NBRM  XBA -> XEU before 1999-01-01
#   NBRM  XBA -> EUR from 1999-01-01         equal to NBRM's own EUR row each day, so these collapse
#
# NBRM's rollups are rebuilt for every affected bucket and coverage is recomputed. The blend tables are left as they
# are. Run `rake blend:rebuild` after deploy; it rebuilds in place and keeps the tables serving.
Sequel.migration do
  up do
    require "currency_summary"

    changes = [
      { from: "XBA", to: "XEU", before: "1999-01-01" },
      { from: "XBA", to: "EUR", since: "1999-01-01" },
    ]
    provider = "NBRM"
    rollups = { weekly_rates: Bucket.week, monthly_rates: Bucket.month }
    scope_for = lambda do |change|
      scope = from(:rates).where(provider:).where(Sequel.|({ base: change[:from] }, { quote: change[:from] }))
      scope = scope.where(Sequel[:date] >= change[:since]) if change[:since]
      change[:before] ? scope.where(Sequel[:date] < change[:before]) : scope
    end

    transaction do
      next if changes.all? { |change| scope_for[change].empty? }

      buckets = Hash.new { |hash, key| hash[key] = [] }
      changes.each do |change|
        scope = scope_for[change]
        rollups.each { |table, bucket| buckets[table] |= scope.select_map(bucket) }

        scope.all.each do |row|
          key = row.slice(:provider, :date, :base, :quote)
          target = key.transform_values { |value| value == change[:from] ? change[:to] : value }
          components = row.slice(:mid, :bid, :ask)

          existing = from(:rates).where(target).first
          if existing
            # Equal duplicates collapse. A disagreement needs source evidence, not an arbitrary winner.
            unless existing.slice(:mid, :bid, :ask) == components
              raise "#{provider}: conflicting #{change[:from]}/#{change[:to]} components on #{row[:date]}"
            end

            from(:rates).where(key).delete
          else
            from(:rates).where(key).update(target.slice(:base, :quote))
          end
        end
      end

      buckets.each do |table, dates|
        bucket = rollups.fetch(table)
        from(table).where(provider:, bucket_date: dates).delete
        from(table).insert(
          [:bucket_date, :provider, :base, :quote, :rate],
          from(:rates).where(provider:).where(bucket => dates)
            .select(bucket, :provider, :base, :quote, Sequel.function(:avg, :rate))
            .group(:provider, :base, :quote, bucket),
        )
      end
      CurrencySummary.refresh(self, ["XBA", "XEU", "EUR"], provider:)
    end
  end

  down do
    # Irreversible: the adapter no longer emits XBA.
  end
end
