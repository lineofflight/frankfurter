# frozen_string_literal: true

require "json"
require "net/http"

require "providers/base"

module Providers
  # Federal Reserve Economic Data (FRED). Publishes daily H.10 exchange rates.
  # Most series are quoted as foreign currency per USD. A few (AUD, EUR, GBP, NZD)
  # are quoted as USD per foreign currency and stored with the foreign currency as base.
  class FRED < Base
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

    def key = "FRED"
    def name = "Federal Reserve"
    def base = "USD"

    def current
      return no_key unless api_key

      @dataset = SERIES.flat_map do |series_id, (quote, series_base)|
        sleep(0.2)
        fetch_series(series_id, quote, series_base, limit: 5, sort_order: "desc")
      end
      last_date = @dataset.max_by { |r| r[:date] }&.dig(:date)
      @dataset = @dataset.select { |r| r[:date] == last_date }
      self
    end

    def historical(start_date: "2000-01-01", end_date: Date.today.to_s)
      return no_key unless api_key

      @dataset = []
      SERIES.each do |series_id, (quote, series_base)|
        sleep(0.2)
        @dataset.concat(fetch_series(
          series_id,
          quote,
          series_base,
          observation_start: start_date,
          observation_end: end_date,
        ))
      end
      self
    end

    private

    def api_key
      ENV["FRED_API_KEY"]
    end

    def no_key
      @dataset = []
      self
    end

    def fetch_series(series_id, quote, series_base, **params)
      url = URI(API_URL)
      url.query = URI.encode_www_form(
        series_id: series_id,
        api_key: api_key,
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
        { provider: key, date:, base: series_base, quote:, rate: }
      end
    rescue JSON::ParserError
      []
    end
  end
end
