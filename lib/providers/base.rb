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

      def backfill
        since = Rate.where(provider: key).max(:date)
        return if since && Date.parse(since.to_s) >= Date.today

        new.fetch(since:).import
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

    def fetch(since: nil)
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
