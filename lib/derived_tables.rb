# frozen_string_literal: true

require "blended_monthly_rate"
require "blended_rate"
require "blended_weekly_rate"
require "bucket"
require "cache"
require "currency_summary"
require "db"
require "log"
require "rate_rollup"
require "rate_spike"

# Everything stored beside rates can be rebuilt from them: currency coverage and the catalogue, spike flags, provider
# rollups and the stored blends. Migrations up to 049 repair rates in place but no longer refresh what derives from
# them, so a database upgraded past them rebuilds every derived table once (db:migrate). Rates are never deleted or
# refetched: some sources keep a rolling archive, block us, or throttle, so stored history can be all there is.
module DerivedTables
  class << self
    def rebuild(db = DB)
      timed("derived tables") do
        timed("currency coverage") { rebuild_coverage(db) }
        timed("spike flags") { rebuild_spikes(db) }
        timed("rollups") { rebuild_rollups(db) }
        [BlendedRate, BlendedWeeklyRate, BlendedMonthlyRate].each { |model| timed(model.table_name) { model.rebuild } }
      end
    ensure
      # Rebuilds change served values, so cached responses must not outlive them.
      Cache.purge
    end

    private

    def rebuild_coverage(db)
      db.transaction do
        [:currency_coverages, :currency_exclusions, :currencies].each { |table| db[table].delete }
        db[:rates].select(:dataset_key).distinct.select_map(:dataset_key).each do |key|
          rows = db[:rates].where(dataset_key: key)
          codes = rows.distinct.select_map(:base) | rows.distinct.select_map(:quote)
          CurrencySummary.refresh(db, codes, dataset: key)
        end
      end
    end

    def rebuild_spikes(db)
      db.transaction do
        db[:rate_spikes].delete
        db[:rate_spikes].insert([:dataset_key, :date, :base, :quote], RateSpike.detect(db[:rates]))
      end
    end

    def rebuild_rollups(db)
      db.transaction do
        { weekly_rates: Bucket.week, monthly_rates: Bucket.month }.each do |table, bucket|
          db[table].delete
          db[table].insert(RateRollup::COLUMNS, RateRollup.source(db[:rates], bucket))
        end
      end
    end

    def timed(name)
      started = Process.clock_gettime(Process::CLOCK_MONOTONIC)
      Log.info("rebuild: #{name}")
      yield
      Log.info("rebuild: #{name} done in #{(Process.clock_gettime(Process::CLOCK_MONOTONIC) - started).round(1)}s")
    end
  end
end
