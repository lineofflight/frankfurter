# frozen_string_literal: true

require "bucket"
require "cache"
require "db"
require "json"
require "log"
require "money/currency"
require "currency_coverage"
require "provider/adapters/adapter"
require "rate"

class Provider < Sequel::Model(:providers)
  plugin :static_cache

  one_to_many :rates, key: :provider
  one_to_many :currency_coverages, key: :provider_key
  many_to_many :currencies, join_table: :currency_coverages, left_key: :provider_key, right_key: :iso_code

  EXCLUDED_QUOTES = ["XDR"].freeze

  class << self
    def seed
      path = File.expand_path("../db/seeds/providers.json", __dir__)
      data = JSON.parse(File.read(path))
      dataset.delete
      dataset.multi_insert(data)
      load_cache
    end
  end

  def adapter
    Adapters.const_get(key)
  end

  def start_date
    currencies.map { |c| c.start_date.to_s }.min
  end

  def end_date
    currencies.map { |c| c.end_date.to_s }.max
  end

  def last_synced
    # There seems to be no Sequel-level way to make the aggregate auto-cast.
    date = Rate.where(provider: key).max(:date)
    Date.parse(date) if date
  end

  def backfill(after: last_synced || coverage_start)
    Log.info("#{key}: backfilling from #{after || "start"}")
    adapter.fetch_each(after:) do |records|
      records.reject! { |r| [r[:base], r[:quote]].any? { |c| !Money::Currency.find(c) || EXCLUDED_QUOTES.include?(c) } }
      records.each { |r| r[:provider] = key }

      inserted = db.transaction do
        before = db.get(Sequel.lit("total_changes()"))
        Rate.dataset.insert_conflict(target: [:provider, :date, :base, :quote]).multi_insert(records)
        count = db.get(Sequel.lit("total_changes()")) - before
        if count > 0
          affected_currencies = records.flat_map { |r| [r[:base], r[:quote]] }.uniq
          refresh_rollups(records.map { |r| r[:date] }.uniq)
          refresh_currency_summaries(affected_currencies)
        end
        count
      end

      Log.info("#{key}: inserted #{inserted} rates")
      next if inserted.zero?

      Cache.purge
      db.run("PRAGMA optimize")
    end
  rescue Adapters::Adapter::ApiKeyMissing
    Log.warn("#{key}: no api key, skipping")
  rescue Errno::ECONNRESET, Net::OpenTimeout, Net::ReadTimeout, SocketError => e
    Log.error("#{key}: #{e.class}")
  end

  private

  def refresh_rollups(dates)
    refresh_rollup(:weekly_rates, Bucket.week, dates)
    refresh_rollup(:monthly_rates, Bucket.month, dates)
  end

  def refresh_currency_summaries(iso_codes)
    # Upsert currency coverages — pairs known from data, no query needed
    pairs = iso_codes.map { |c| { provider_key: key, iso_code: c } }
    CurrencyCoverage.dataset.insert_conflict.multi_insert(pairs)

    # Upsert currencies — scoped MIN/MAX per affected iso_code
    iso_codes.each do |code|
      dates = db[:rates].where(Sequel.|({ quote: code }, { base: code }))
        .select { [min(date).as(start_date), max(date).as(end_date)] }.first # rubocop:disable Performance/Detect
      next unless dates

      db[:currencies].insert_conflict(target: :iso_code, update: {
        start_date: Sequel.function(:min, Sequel[:currencies][:start_date], dates[:start_date]),
        end_date: Sequel.function(:max, Sequel[:currencies][:end_date], dates[:end_date]),
      }).insert(iso_code: code, start_date: dates[:start_date], end_date: dates[:end_date])
    end
  end

  def refresh_rollup(table, bucket_expr, dates)
    buckets = db[:rates]
      .where(provider: key, date: dates)
      .select_map(bucket_expr)
      .uniq

    return if buckets.empty?

    db[table].where(provider: key, bucket_date: buckets).delete

    db[table].insert(
      [:bucket_date, :provider, :base, :quote, :rate],
      db[:rates]
        .where(provider: key)
        .where(bucket_expr => buckets)
        .select(bucket_expr, :provider, :base, :quote, Sequel.function(:avg, :rate))
        .group(:provider, :base, :quote, bucket_expr),
    )
  end
end
