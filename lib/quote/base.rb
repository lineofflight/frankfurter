# frozen_string_literal: true

require "roundable"

module Quote
  class Base
    include Roundable

    DEFAULT_BASE = "EUR"

    attr_reader :amount, :base, :date, :symbols, :result

    def initialize(date:, amount: 1.0, base: "EUR", symbols: nil)
      @date = date
      @amount = amount
      @base = base
      @symbols = symbols
      @result = {}
    end

    def perform
      return false if result.frozen?

      prepare_rates
      rebase_rates if must_rebase?
      result.freeze
    end

    def must_rebase?
      base != "EUR"
    end

    def should_round?
      amount != 1.0 || must_rebase?
    end

    def formatted
      raise NotImplementedError
    end

    def not_found?
      result.empty?
    end

    def cache_key
      raise NotImplementedError
    end

    private

    def data
      @data ||= fetch_data
    end

    def fetch_data
      raise NotImplementedError
    end

    def prepare_rates
      data.each_with_object(result) do |currency, result|
        date = currency[:date].to_date.to_s
        result[date] ||= {}
        rate = amount * currency[:rate]
        result[date][currency[:iso_code]] = should_round? ? round(rate) : rate
      end
    end

    def rebase_rates
      result.each do |date, rates|
        rates["EUR"] = amount if symbols.nil? || symbols.include?("EUR")
        divisor = rates.delete(base)
        if divisor.nil? || rates.empty?
          result.delete(date)
        else
          result[date] = rates.sort
            .map! do |iso_code, rate|
            [iso_code, round(amount * rate / divisor)]
          end
            .to_h
        end
      end
    end
  end
end
