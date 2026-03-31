# frozen_string_literal: true

require "json"
require "net/http"

require "providers/base"

module Providers
  # Bank of Japan. Fetches daily spot exchange rates for USD/JPY and EUR/USD
  # from the Tokyo market via the BOJ Statistics API. No authentication required.
  class BOJ < Base
    API_URL = "https://www.stat-search.boj.or.jp/api/v1/getDataCode"

    # series_code => { base:, quote: }
    SERIES = {
      "FXERD04" => { base: "USD", quote: "JPY" },
      "FXERD34" => { base: "EUR", quote: "USD" },
    }.freeze

    class << self
      def key = "BOJ"
      def name = "Bank of Japan"
      def earliest_date = Date.new(1998, 1, 5)
    end

    def fetch(since: nil, upto: nil)
      url = URI(API_URL)
      params = {
        format: "json",
        lang: "en",
        db: "FM08",
        code: SERIES.keys.join(","),
      }
      if since
        params[:startDate] = since.strftime("%Y%m")
        params[:endDate] = (upto || Date.today).strftime("%Y%m")
      end
      url.query = URI.encode_www_form(params)

      response = Net::HTTP.get(url)
      @dataset = parse(response)
      self
    rescue Net::OpenTimeout, Net::ReadTimeout, Socket::ResolutionError, OpenSSL::SSL::SSLError, JSON::ParserError
      @dataset = []
      self
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
          { provider: key, date:, base: meta[:base], quote: meta[:quote], rate: }
        rescue ArgumentError, TypeError
          nil
        end
      end
    end
  end
end
