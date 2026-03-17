# frozen_string_literal: true

require "rate"

module Providers
  class << self
    def all
      @all ||= []
    end
  end

  class Base
    class << self
      def inherited(subclass)
        super
        Providers.all << subclass
      end
    end

    attr_reader :dataset, :logger

    def initialize(dataset: [], logger: LOGGER)
      @dataset = dataset
      @logger = logger
    end

    def key = raise(NotImplementedError)
    def name = raise(NotImplementedError)
    def base = raise(NotImplementedError)

    def current
      raise NotImplementedError
    end

    def historical
      raise NotImplementedError
    end

    def import
      logger.info("#{key}: started")
      Rate.dataset.insert_conflict(target: [:provider, :date, :quote]).multi_insert(dataset) unless dataset.empty?
      logger.info("#{key}: imported #{count.to_s.reverse.gsub(/(\d{3})(?=\d)/, '\\1,').reverse} rates")

      self
    end

    def count
      dataset.size
    end
  end
end
