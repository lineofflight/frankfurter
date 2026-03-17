# frozen_string_literal: true

require "logger"
require "rate"

module Providers
  class << self
    def all
      @all ||= []
    end

    def logger
      @logger ||= Logger.new($stdout)
    end

    attr_writer :logger
  end

  class Base
    class << self
      def inherited(subclass)
        super
        Providers.all << subclass
      end
    end

    attr_reader :dataset, :logger

    def initialize(dataset: [], logger: Providers.logger)
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
      logger.info("#{key}: done (#{dataset.size} rates)")

      self
    end
  end
end
