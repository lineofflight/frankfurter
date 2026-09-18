# frozen_string_literal: true

require "date"
require "money/currency"
require "sequel"

require "bucket"
require "currency_summary"
require "defunct_currency"
require "nascent_currency"

# Ingest policy keeps provider-published rows, rejecting invalid values and implausible future dates. Premature
# successor codes are relabelled with their known predecessor; currency eligibility is applied when blending.
module RateValidation
  RATE_TABLES = [:rates, :weekly_rates, :monthly_rates].freeze

  # Bucket precision per rollup table; the daily `rates` table has none.
  PRECISION = { weekly_rates: :week, monthly_rates: :month }.freeze

  # A missing or non-positive rate.
  module NonPositiveRate
    class << self
      def reject?(record, _date, **)
        record[:rate].nil? || record[:rate] <= 0
      end
    end
  end

  # A date implausibly far in the future.
  module FutureDate
    # How far ahead of today a fetched rate may be dated. Genuine forward value dates (far-eastern time zones, T+1
    # conventions) sit within a day or two; anything beyond is an upstream typo or a stray row. Storing it would hijack
    # last_synced (= max date) and freeze backfill behind an unreachable cursor. An adapter that publishes ahead of its
    # period (HMRC: next month's customs rates, this month) declares a lead, which extends the horizon by that much.
    MAX_FUTURE_DRIFT = 2

    class << self
      def horizon(lead_days = 0)
        Date.today + MAX_FUTURE_DRIFT + lead_days
      end

      def reject?(_record, date, lead_days: 0)
        date > horizon(lead_days)
      end

      # Rollup buckets anchor to a fixed weekday (weekly) or the first of the month (monthly), so the live period's
      # bucket can sit a few days ahead of the latest date it actually summarises. Comparing such a bucket against the
      # raw daily horizon wrongly purges the current rollup; bucket the horizon to the table's precision so only buckets
      # whose whole period is beyond the horizon are dropped. A provider with a lead gets its own, later bound.
      def reject_scope(dataset, date_column, precision = nil)
        leads = provider_leads
        default = Sequel[date_column] > horizon_bound(precision, horizon)
        default = Sequel.&(default, Sequel.~(provider: leads.keys)) unless leads.empty?
        conditions = leads.map do |key, lead_days|
          Sequel.&({ provider: key }, Sequel[date_column] > horizon_bound(precision, horizon(lead_days)))
        end
        dataset.where(Sequel.|(default, *conditions))
      end

      private

      # Leads by provider key, for providers whose adapter declares one. Keys without an adapter (test fixtures) have
      # none.
      def provider_leads
        require "provider"
        require "provider/adapters"
        Provider.all.filter_map do |provider|
          next unless Provider::Adapters.const_defined?(provider.key)

          lead_days = Provider::Adapters.const_get(provider.key).lead_days
          [provider.key, lead_days] unless lead_days.zero?
        end.to_h
      end

      def horizon_bound(precision, date)
        case precision
        when :week then Bucket.week(date.to_s)
        when :month then Bucket.month(date.to_s)
        else date.to_s
        end
      end
    end
  end

  RULES = [NonPositiveRate, FutureDate].freeze
  PURGEABLE = [FutureDate].freeze

  class << self
    # Mutates `records`, dropping every row that any rule rejects. `lead_days` is the fetching adapter's publication
    # lead (see Adapter.lead_days).
    def reject!(records, lead_days: 0)
      records.reject! { |record| rejected?(record, lead_days:) }
    end

    def rejected?(record, lead_days: 0)
      date = normalize_date(record[:date])
      return true if RULES.any? { |rule| rule.reject?(record, date, lead_days:) }

      [:base, :quote].each do |side|
        entry = NascentCurrency.find(record[side])
        next unless entry && date < entry.inception_date
        return true unless entry.predecessor

        record[side] = entry.predecessor
      end
      false
    end

    # Delete rejected source rows and repair affected provider buckets from surviving dailies, then rebuild summaries.
    # Returns counts rejected by each table's purge rules; replacing a provider bucket does not add to those counts.
    def purge(db)
      require "rate"
      require "provider"

      totals = RATE_TABLES.to_h { |table| [table, 0] }
      affected = []
      repairs = PRECISION.keys.to_h { |table| [table, {}] }

      db.transaction(savepoint: true, **(db.in_transaction? ? {} : { mode: :immediate })) do
        RATE_TABLES.each do |table|
          date_column = table == :rates ? :date : :bucket_date
          precision = PRECISION[table]

          PURGEABLE.each do |rule|
            scope = rule.reject_scope(db[table], date_column, precision)
            affected.concat(scope.select_map(:base), scope.select_map(:quote))
            if precision
              scope.select(:provider, :bucket_date).distinct.each do |row|
                (repairs[table][row[:provider]] ||= []) << row[:bucket_date]
              end
            else
              capture_rollup_repairs(scope, repairs)
            end
            totals[table] += scope.delete
          end
        end

        repair_rollups(db, repairs)
        rebuild_summaries(db, affected.uniq) unless affected.empty?
      end

      totals
    end

    private

    def normalize_date(value)
      value.is_a?(Date) ? value : Date.parse(value.to_s)
    end

    def capture_rollup_repairs(scope, repairs)
      PRECISION.each do |table, precision|
        bucket = Bucket.expression(precision).as(:bucket_date)
        scope.select(:provider, bucket).distinct.each do |row|
          (repairs[table][row[:provider]] ||= []) << row[:bucket_date]
        end
      end
    end

    def repair_rollups(db, repairs)
      repairs.each do |table, providers|
        bucket = Bucket.expression(PRECISION.fetch(table))
        providers.each do |provider, dates|
          dates.uniq!
          # Captured dates drive deletion even when no daily rows survive. Rebuild retained periods from the remaining
          # observations after purging any wholly future buckets.
          db[table].where(provider:, bucket_date: dates).delete
          db[table].insert(
            [:bucket_date, :provider, :base, :quote, :rate],
            db[:rates].where(provider:).where(bucket => dates)
              .select(bucket, :provider, :base, :quote, Sequel.function(:avg, :rate))
              .group(:provider, :base, :quote, bucket),
          )
          blended_table = :"blended_#{table}"
          if !Provider.non_blending_keys.include?(provider) && db.table_exists?(blended_table)
            db[blended_table].where(bucket_date: dates).delete
          end
        end
      end
    end

    def rebuild_summaries(db, iso_codes)
      CurrencySummary.refresh(db, iso_codes)
    end
  end
end
