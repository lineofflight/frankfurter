# frozen_string_literal: true

require "cache"
require "logger"
require "money"
require "rate"

module Providers
  class << self
    attr_reader :logger

    def all
      @all ||= []
    end
  end

  @logger = Logger.new($stdout)
  @logger.level = Logger::WARN if ENV["APP_ENV"] == "test"

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

    attr_reader :dataset, :logger

    def initialize(dataset: [], logger: Providers.logger)
      @dataset = dataset
      @logger = logger
      logger.info("#{key}: started")
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
      before = Rate.where(provider: key).count
      Rate.dataset.insert_conflict(target: [:provider, :date, :base, :quote]).multi_insert(dataset) unless dataset.empty?
      inserted = Rate.where(provider: key).count - before
      logger.info("#{key}: imported #{inserted} rates")
      if inserted > 0 && Cache.purge
        logger.info("#{key}: purged cache")
      end

      self
    end

    def count
      dataset.size
    end
  end
end
