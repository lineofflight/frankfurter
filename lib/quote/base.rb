# frozen_string_literal: true

require "roundable"

module Quote
  class Base
    include Roundable

    DEFAULT_BASE = "EUR"

    attr_reader :amount, :base, :date, :symbols, :result, :source

    def initialize(params)
      @amount = params[:amount] || 1.0
      @base = params[:base] || DEFAULT_BASE
      @date = params[:date]
      @symbols = params[:symbols]
      @source = params.key?(:source) ? resolve_source(params[:base] || DEFAULT_BASE, params[:source]) : nil
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

    def resolve_source(from_currency, source_param = nil)
      require "source"
      require "currency"

      if source_param
        source = Source[source_param]
        raise ArgumentError, "Source #{source_param} not found" unless source

        source_param
      else
        source = Source.first(base_currency: from_currency)
        raise ArgumentError, "No source found for currency #{from_currency}" unless source

        has_data = Currency.by_source(source.code).limit(1).any?
        raise ArgumentError, "No data available for #{from_currency} from #{source.code} source" unless has_data

        source.code
      end
    end

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
        result[date][currency[:iso_code]] = round(amount * currency[:rate])
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
