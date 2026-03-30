# frozen_string_literal: true

require "cache"
require "log"
require "money/currency"
require "provider"
require "rate"

module Providers
  class << self
    def all
      @all ||= []
    end

    def enabled
      seeded = Provider.map(&:key)
      all.select { |p| seeded.include?(p.key) }
    end
  end

  class Base
    class << self
      def inherited(subclass)
        super
        Providers.all << subclass
      end

      def key = raise(NotImplementedError)
      def name = raise(NotImplementedError)
      def earliest_date = nil

      def backfill(range: nil)
        since = Rate.where(provider: key).max(:date)
        since = Date.parse(since.to_s) if since
        return if since && since >= Date.today

        since ||= earliest_date
        each_period(since, range) do |period_since, period_upto|
          period = period_upto ? "#{period_since}..#{period_upto}" : "#{period_since}.."
          Log.info("#{key}: fetching #{period}")
          new.fetch(since: period_since, upto: period_upto).import
        end
      end

      private

      def each_period(since, range)
        unless range && since
          yield(since, nil)
          return
        end

        cursor = since
        loop do
          period_upto = cursor + range - 1
          if period_upto >= Date.today
            yield(cursor, nil)
            break
          end
          yield(cursor, period_upto)
          cursor = period_upto + 1
        end
      end
    end

    attr_reader :dataset

    def initialize(dataset: [])
      @dataset = dataset
    end

    def key = self.class.key
    def name = self.class.name

    def fetch(since: nil, upto: nil)
      raise NotImplementedError
    end

    # Precious metals and IMF instruments — recognised by Money gem but not currencies
    EXCLUDED_QUOTES = ["XAU", "XAG", "XPT", "XPD", "XDR"].freeze

    def import
      @dataset = dataset.reject do |r|
        [r[:base], r[:quote]].any? { |c| !Money::Currency.find(c) || EXCLUDED_QUOTES.include?(c) }
      end
      before = DB["SELECT total_changes()"].single_value
      Rate.dataset.insert_conflict(target: [:provider, :date, :base, :quote]).multi_insert(dataset) unless dataset.empty?
      inserted = DB["SELECT total_changes()"].single_value - before
      Log.info("#{key}: imported #{inserted} rates")
      if inserted > 0
        DB.run("PRAGMA optimize")
        Log.info("#{key}: purged cache") if Cache.purge
      end

      self
    end

    def count
      dataset.size
    end
  end
end
