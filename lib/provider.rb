# frozen_string_literal: true

require "cache"
require "db"
require "json"
require "log"
require "money/currency"
require "provider/adapters/adapter"
require "rate"

class Provider < Sequel::Model(:providers)
  plugin :static_cache

  one_to_many :rates, key: :provider, primary_key: :key

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

      before = db.get(Sequel.lit("total_changes()"))
      Rate.dataset.insert_conflict(target: [:provider, :date, :base, :quote]).multi_insert(records)
      inserted = db.get(Sequel.lit("total_changes()")) - before

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
end
