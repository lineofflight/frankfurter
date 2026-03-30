# frozen_string_literal: true

require "json"
require "net/http"

require "providers/base"

module Providers
  # Sveriges Riksbank. Fetches daily exchange rates for ~29 currencies
  # against the Swedish krona (SEK) via the SWEA API. Each currency
  # requires a separate request (series pattern: SEK{currency}PMI).
  # Rate limit: ~5 requests per minute; we sleep between requests.
  class RB < Base
    BASE_URL = "https://api.riksbank.se/swea/v1/Observations"
    EARLIEST_DATE = Date.new(1993, 1, 4)

    SERIES = [
      "AUD",
      "BRL",
      "CAD",
      "CHF",
      "CNY",
      "CZK",
      "DKK",
      "EUR",
      "GBP",
      "HKD",
      "HUF",
      "IDR",
      "ILS",
      "INR",
      "ISK",
      "JPY",
      "KRW",
      "MXN",
      "MYR",
      "NOK",
      "NZD",
      "PHP",
      "PLN",
      "RON",
      "SGD",
      "THB",
      "TRY",
      "USD",
      "ZAR",
    ].freeze

    class << self
      def key = "RB"
      def name = "Sveriges Riksbank"
      def earliest_date = EARLIEST_DATE
    end

    def fetch(since: nil, upto: nil)
      start_date = since || EARLIEST_DATE
      end_date = upto || Date.today

      @dataset = []
      SERIES.each_with_index do |currency, index|
        sleep(1) if index > 0
        records = fetch_series(currency, start_date, end_date)
        @dataset.concat(records)
      end

      self
    rescue Net::OpenTimeout, Net::ReadTimeout, Socket::ResolutionError
      @dataset ||= []
      self
    end

    def parse(json, currency:)
      data = json.is_a?(String) ? JSON.parse(json) : json

      data.filter_map do |obs|
        date_str = obs["date"]
        value = obs["value"]
        next unless date_str && value

        rate = Float(value)
        next if rate.zero?

        { provider: key, date: Date.parse(date_str), base: currency, quote: "SEK", rate: }
      rescue ArgumentError, TypeError
        nil
      end
    end

    private

    def fetch_series(currency, start_date, end_date)
      series_id = "SEK#{currency}PMI"
      uri = URI("#{BASE_URL}/#{series_id}/#{start_date}/#{end_date}")
      response = Net::HTTP.get(uri)
      parse(response, currency: currency)
    rescue JSON::ParserError
      []
    end
  end
end
