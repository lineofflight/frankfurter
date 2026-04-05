# frozen_string_literal: true

require "json"
require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Bank of Japan. Fetches daily spot exchange rates for USD/JPY and EUR/USD
    # from the Tokyo market via the BOJ Statistics API. No authentication required.
    class BOJ < Adapter
      API_URL = "https://www.stat-search.boj.or.jp/api/v1/getDataCode"

      # series_code => { base:, quote: }
      SERIES = {
        "FXERD04" => { base: "USD", quote: "JPY" },
        "FXERD34" => { base: "EUR", quote: "USD" },
      }.freeze

      def fetch(after: nil, upto: nil)
        effective_after = after
        effective_upto = upto || Date.today

        url = URI(API_URL)
        params = {
          format: "json",
          lang: "en",
          db: "FM08",
          code: SERIES.keys.join(","),
          startDate: effective_after.strftime("%Y%m"),
          endDate: effective_upto.strftime("%Y%m"),
        }
        url.query = URI.encode_www_form(params)

        response = Net::HTTP.get(url)
        raw = parse(response)
        @dataset = raw.select { |r| r[:date].between?(effective_after, effective_upto) }
      end

      def parse(json)
        data = JSON.parse(json)
        resultset = data["RESULTSET"] || []

        resultset.flat_map do |series|
          meta = SERIES[series["SERIES_CODE"]]
          next [] unless meta

          values_data = series.dig("VALUES") || {}
          dates = values_data["SURVEY_DATES"] || []
          rates = values_data["VALUES"] || []

          dates.zip(rates).filter_map do |raw_date, value|
            next if value.nil?

            rate = Float(value)
            next if rate.zero?

            date = Date.strptime(raw_date.to_s, "%Y%m%d")
            { date:, base: meta[:base], quote: meta[:quote], rate: }
          end
        end
      end
    end
  end
end
