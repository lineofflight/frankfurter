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

    attr_reader :dataset

    def initialize(dataset: [])
      @dataset = dataset
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
      records = dataset.flat_map do |day|
        day[:rates].map do |quote, rate|
          { date: day[:date], provider: key, base:, quote:, rate: }
        end
      end
      Rate.dataset.insert_conflict(target: [:provider, :date, :quote]).multi_insert(records)

      self
    end
  end
end
