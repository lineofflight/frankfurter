# frozen_string_literal: true

require "date"
require "money/currency"
require "sequel"

require "bucket"
require "defunct_currency"

# Ingest-validation policy for fetched rate records. Each rule answers one question — "is this row acceptable?" Rules
# answer it for an in-memory record via `reject?`; the rules that can leave bad rows already stored also answer it as a
# SQL filter via `reject_scope`, which `purge` applies retroactively. The purge machinery (iterate tables, collect
# affected codes, delete, rebuild summaries) lives here once; a rule contributes only its predicate.
#
# Two date rules share the same shape (an upper bound on a row's date): TerminalDate is per-currency from a curated
# list; FutureDate is universal.
module RateValidation
  RATE_TABLES = [:rates, :weekly_rates, :monthly_rates].freeze

  # Bucket precision per rollup table; the daily `rates` table has none.
  PRECISION = { weekly_rates: :week, monthly_rates: :month }.freeze

  # A currency code unknown to the Money::Currency registry.
  module UnknownCurrency
    class << self
      def reject?(record, _date)
        !Money::Currency.find(record[:base]) || !Money::Currency.find(record[:quote])
      end
    end
  end

  # A missing or non-positive rate.
  module NonPositiveRate
    class << self
      def reject?(record, _date)
        record[:rate].nil? || record[:rate] <= 0
      end
    end
  end

  # A date implausibly far in the future.
  module FutureDate
    # How far ahead of today a fetched rate may be dated. Genuine forward value dates (far-eastern time zones, T+1
    # conventions) sit within a day or two; anything beyond is an upstream typo or a stray row. Storing it would hijack
    # last_synced (= max date) and freeze backfill behind an unreachable cursor.
    MAX_FUTURE_DRIFT = 2

    class << self
      def horizon
        Date.today + MAX_FUTURE_DRIFT
      end

      def reject?(_record, date)
        date > horizon
      end

      # Rollup buckets anchor to a fixed weekday (weekly) or the first of the month (monthly), so the live period's
      # bucket can sit a few days ahead of the latest date it actually summarises. Comparing such a bucket against the
      # raw daily horizon wrongly purges the current rollup; bucket the horizon to the table's precision so only buckets
      # whose whole period is beyond the horizon are dropped.
      def reject_scope(dataset, date_column, precision = nil)
        dataset.where(Sequel[date_column] > horizon_bound(precision))
      end

      private

      def horizon_bound(precision)
        case precision
        when :week then Bucket.week(horizon.to_s)
        when :month then Bucket.month(horizon.to_s)
        else horizon.to_s
        end
      end
    end
  end

  # A row dated on or after a defunct currency's terminal date.
  module TerminalDate
    class << self
      def reject?(record, date)
        DefunctCurrency.expired?(record[:base], date) || DefunctCurrency.expired?(record[:quote], date)
      end

      # Exact on daily rows; on rollups it compares the bucket anchor (not the period start), so the one week straddling
      # a terminal date may be off by one. Harmless: rollup rebuild re-derives it from the exactly-filtered dailies.
      def reject_scope(dataset, date_column, _precision = nil)
        conditions = DefunctCurrency.all.map do |entry|
          Sequel.&(
            { date_column => entry.terminal_date.to_s.. },
            Sequel.|({ base: entry.iso_code }, { quote: entry.iso_code }),
          )
        end
        return dataset.where(false) if conditions.empty?

        dataset.where(Sequel.|(*conditions))
      end
    end
  end

  RULES = [UnknownCurrency, NonPositiveRate, FutureDate, TerminalDate].freeze
  PURGEABLE = [FutureDate, TerminalDate].freeze

  class << self
    # Mutates `records`, dropping every row that any rule rejects.
    def reject!(records)
      records.reject! { |record| rejected?(record) }
    end

    def rejected?(record)
      date = normalize_date(record[:date])
      RULES.any? { |rule| rule.reject?(record, date) }
    end

    # Retroactively delete already-stored rows that a purgeable rule rejects, then rebuild the currency and coverage
    # summaries for affected codes. Returns per-table deletion counts.
    def purge(db)
      require "rate"

      totals = RATE_TABLES.to_h { |table| [table, 0] }
      affected = []

      db.transaction do
        RATE_TABLES.each do |table|
          date_column = table == :rates ? :date : :bucket_date
          precision = PRECISION[table]

          PURGEABLE.each do |rule|
            scope = rule.reject_scope(db[table], date_column, precision)
            affected.concat(scope.select_map(:base), scope.select_map(:quote))
            totals[table] += scope.delete
          end
        end

        rebuild_summaries(db, affected.uniq) unless affected.empty?
      end

      totals
    end

    private

    def normalize_date(value)
      value.is_a?(Date) ? value : Date.parse(value.to_s)
    end

    def rebuild_summaries(db, iso_codes)
      iso_codes.each do |code|
        db[:currency_coverages].where(iso_code: code).delete

        db[:rates]
          .where(Sequel.|({ base: code }, { quote: code }))
          .group(:provider)
          .select(
            :provider,
            Sequel.function(:min, :date).as(:start_date),
            Sequel.function(:max, :date).as(:end_date),
          ).each do |row|
            db[:currency_coverages].insert(
              provider_key: row[:provider],
              iso_code: code,
              start_date: row[:start_date],
              end_date: row[:end_date],
            )
          end

        db[:currencies].where(iso_code: code).delete
        global = db[:currency_coverages]
          .where(iso_code: code)
          .select(
            Sequel.function(:min, :start_date).as(:start_date),
            Sequel.function(:max, :end_date).as(:end_date),
          ).first

        next unless global && global[:start_date]

        db[:currencies].insert(
          iso_code: code,
          start_date: global[:start_date],
          end_date: global[:end_date],
        )
      end
    end
  end
end
