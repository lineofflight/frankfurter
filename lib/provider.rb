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
      dir = File.expand_path("../db/seeds/providers", __dir__)
      data = Dir["#{dir}/*.json"].sort.map { |f| JSON.parse(File.read(f)) }
      dataset.delete
      dataset.multi_insert(data)
      load_cache
    end
  end

  def adapter
    Adapters.const_get(key)
  end

  def start_date
    currency_coverages.map { |c| c.start_date.to_s }.min
  end

  def end_date
    currency_coverages.map { |c| c.end_date.to_s }.max
  end

  def last_synced
    # There seems to be no Sequel-level way to make the aggregate auto-cast.
    date = Rate.where(provider: key).max(:date)
    Date.parse(date) if date
  end

  def publishes_missed(reference_date: Date.today)
    return unless publish_days

    last = end_date
    return 0 unless last

    wdays = parse_publish_days(publish_days)
    date = Date.parse(last) + 1
    count = 0
    while date < reference_date
      count += 1 if wdays.include?(date.wday)
      date += 1
    end
    count
  end

  def backfill(after: last_synced || coverage_start)
    if after && after >= Date.today
      Log.info("#{key}: up to date")
      return
    end

    Log.info("#{key}: backfilling from #{after || "start"}")
    fetched = false
    adapter.fetch_each(after:) do |records|
      fetched = true
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
    Log.info("#{key}: fetched no records") unless fetched
  rescue Adapters::Adapter::Unavailable => e
    Log.warn("#{key}: #{e.message}, skipping")
  rescue *Adapters::Adapter::TRANSIENT_ERRORS => e
    Log.error("#{key}: #{e.class}")
  end

  private

  def parse_publish_days(spec)
    if spec.include?("-")
      from, to = spec.split("-").map(&:to_i)
      (from..to).to_a
    else
      [spec.to_i]
    end
  end

  def refresh_rollups(dates)
    refresh_rollup(:weekly_rates, Bucket.week, dates)
    refresh_rollup(:monthly_rates, Bucket.month, dates)
  end

  def refresh_currency_summaries(iso_codes)
    iso_codes.each do |code|
      dates = db[:rates].where(provider: key)
        .where(Sequel.|({ quote: code }, { base: code }))
        .select { [min(date).as(start_date), max(date).as(end_date)] }.first # rubocop:disable Performance/Detect
      next unless dates

      # Upsert coverage with per-provider date range
      db[:currency_coverages].insert_conflict(target: [:provider_key, :iso_code], update: {
        start_date: Sequel.function(:min, Sequel[:currency_coverages][:start_date], dates[:start_date]),
        end_date: Sequel.function(:max, Sequel[:currency_coverages][:end_date], dates[:end_date]),
      }).insert(provider_key: key, iso_code: code, start_date: dates[:start_date], end_date: dates[:end_date])

      # Upsert global currency date range
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
