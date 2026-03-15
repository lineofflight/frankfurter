# frozen_string_literal: true

require "currency"

module Providers
  class Base
    attr_reader :dataset

    def initialize(dataset: [])
      @dataset = dataset
    end

    def key = raise(NotImplementedError)
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
          { date: day[:date], source: key, base:, quote:, rate: }
        end
      end
      Currency.dataset.insert_conflict(target: [:source, :date, :quote]).multi_insert(records)

      self
    end
  end
end
