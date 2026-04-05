# frozen_string_literal: true

require "json"
require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Federal Reserve Economic Data (FRED). Publishes daily H.10 exchange rates.
    # Most series are quoted as foreign currency per USD. A few (AUD, EUR, GBP, NZD)
    # are quoted as USD per foreign currency and stored with the foreign currency as base.
    # The H.10 is a curated policy publication — these are all the series available.
    class FRED < Adapter
      API_URL = "https://api.stlouisfed.org/fred/series/observations"

      # series_id => [quote, base]
      SERIES = {
        "DEXBZUS" => ["BRL", "USD"],
        "DEXCAUS" => ["CAD", "USD"],
        "DEXCHUS" => ["CNY", "USD"],
        "DEXDNUS" => ["DKK", "USD"],
        "DEXHKUS" => ["HKD", "USD"],
        "DEXINUS" => ["INR", "USD"],
        "DEXJPUS" => ["JPY", "USD"],
        "DEXKOUS" => ["KRW", "USD"],
        "DEXMAUS" => ["MYR", "USD"],
        "DEXMXUS" => ["MXN", "USD"],
        "DEXNOUS" => ["NOK", "USD"],
        "DEXSDUS" => ["SEK", "USD"],
        "DEXSFUS" => ["ZAR", "USD"],
        "DEXSIUS" => ["SGD", "USD"],
        "DEXSLUS" => ["LKR", "USD"],
        "DEXSZUS" => ["CHF", "USD"],
        "DEXTAUS" => ["TWD", "USD"],
        "DEXTHUS" => ["THB", "USD"],
        "DEXUSAL" => ["USD", "AUD"],
        "DEXUSEU" => ["USD", "EUR"],
        "DEXUSNZ" => ["USD", "NZD"],
        "DEXUSUK" => ["USD", "GBP"],
      }.freeze

      class << self
        def api_key = ENV["FRED_API_KEY"] || raise(ApiKeyMissing)
      end

      def fetch(after: nil, upto: nil)
        dataset = []
        params = {}
        params[:observation_start] = after.to_s if after

        SERIES.each do |series_id, (quote, series_base)|
          sleep(0.2)
          dataset.concat(fetch_series(series_id, quote, series_base, **params))
        end

        dataset
      end

      private

      def fetch_series(series_id, quote, series_base, **params)
        url = URI(API_URL)
        url.query = URI.encode_www_form(
          series_id: series_id,
          api_key: self.class.api_key,
          file_type: "json",
          **params,
        )

        response = Net::HTTP.get(url)
        data = JSON.parse(response)

        (data["observations"] || []).filter_map do |obs|
          value = obs["value"]
          next if value == "."

          rate = Float(value)
          next if rate.zero?

          date = Date.parse(obs["date"])
          { date:, base: series_base, quote:, rate: }
        end
      end
    end
  end
end
