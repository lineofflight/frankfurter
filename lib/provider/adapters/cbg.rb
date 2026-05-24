# frozen_string_literal: true

require "json"
require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Central Bank of The Gambia. Publishes "Daily Valuation Rates" — indicative
    # exchange rates for 32 foreign currencies against the Gambian dalasi (GMD).
    #
    # Per-currency JSON time series at
    # https://www.cbg.gm/ajax/indicative-exchange-rates/{ISO}. Each endpoint
    # returns the entire archive as a [[epoch_ms, rate], ...] array, so backfill
    # iterates the known currency list and filters by date in memory. The
    # archive reaches back to 2000-01-07 for USD/EUR/GBP; XOF starts 2005,
    # and the remaining currencies start 2019-11-18.
    #
    # Cadence: weekly (~52/yr) from 2000 to 2023, then business-daily from
    # 2024-01 onward. Faithful relay — gaps from the weekly era are preserved
    # as published.
    #
    # Direction: provider records "1 FOREIGN = X GMD" (e.g. 1 USD = 72.39 GMD),
    # so foreign currency is the base and GMD is the quote, matching other
    # pivot-in-quote adapters (NBG, BBK, CBN).
    #
    # WAUA (West African Unit of Account) is published but is not ISO 4217 and
    # is therefore not requested. Rates are returned at two-decimal precision,
    # which floors very-high-denomination currencies (GNF, VND-equivalents) to
    # 0.01; this matches what CBG publishes via this endpoint.
    class CBG < Adapter
      BASE_URL = "https://www.cbg.gm/ajax/indicative-exchange-rates/"
      # Currencies listed on the CBG Daily Valuation Rates table, excluding the
      # GMD pivot and the non-ISO WAUA composite.
      CURRENCIES = [
        "USD",
        "EUR",
        "GBP",
        "CHF",
        "SEK",
        "CAD",
        "XOF",
        "NOK",
        "DKK",
        "SAR",
        "JPY",
        "AUD",
        "TWD",
        "LKR",
        "THB",
        "PHP",
        "NZD",
        "AED",
        "KWD",
        "NGN",
        "HKD",
        "ZAR",
        "EGP",
        "CNY",
        "BRL",
        "INR",
        "GHS",
        "SLL",
        "TRY",
        "GNF",
        "XDR",
        "SGD",
      ].freeze

      def fetch(after: nil, upto: nil)
        CURRENCIES.flat_map do |code|
          records = fetch_currency(code)
          records = records.select { |r| r[:date] > after } if after
          records = records.select { |r| r[:date] <= upto } if upto
          records
        end
      end

      def parse(json, code)
        data = json.is_a?(String) ? JSON.parse(json) : json
        return [] unless data.is_a?(Array)

        data.filter_map do |entry|
          next unless entry.is_a?(Array) && entry.size == 2

          epoch_ms, value = entry
          next if epoch_ms.nil? || value.nil?

          rate = Float(value)
          next if rate.zero?

          date = Time.at(Integer(epoch_ms) / 1000).utc.to_date
          { date:, base: code, quote: "GMD", rate: }
        end
      end

      private

      def fetch_currency(code)
        url = URI(BASE_URL + code)
        response = Net::HTTP.get(url)
        parse(response, code)
      end
    end
  end
end
