# frozen_string_literal: true

require "json"
require "net/http"

require "providers/base"

module Providers
  # Sveriges Riksbank. Fetches daily exchange rates for ~29 currencies
  # against the Swedish krona (SEK) via the SWEA API. Uses the ByGroup
  # endpoint (group 130) to fetch all currency series in a single request.
  # The ByGroup endpoint has a max 1-year date range.
  class RB < Base
    BASE_URL = "https://api.riksbank.se/swea/v1/Observations/ByGroup/130"
    EARLIEST_DATE = Date.new(1993, 1, 4)

    class << self
      def key = "RB"
      def name = "Sveriges Riksbank"
      def earliest_date = EARLIEST_DATE

      def backfill(range: 365)
        super
      end
    end

    def fetch(since: nil, upto: nil)
      start_date = since || EARLIEST_DATE
      end_date = upto || Date.today

      uri = URI("#{BASE_URL}/#{start_date}/#{end_date}")
      response = Net::HTTP.get(uri)
      @dataset = parse(response)

      self
    rescue Net::OpenTimeout, Net::ReadTimeout, Socket::ResolutionError
      @dataset = []
      self
    end

    def parse(json)
      data = json.is_a?(String) ? JSON.parse(json) : json
      return [] unless data.is_a?(Array)

      data.filter_map do |obs|
        series_id = obs["seriesId"]
        date_str = obs["date"]
        value = obs["value"]
        next unless series_id && date_str && value

        currency = series_id.delete_prefix("SEK").delete_suffix("PMI")
        next if currency == "ETT"

        rate = Float(value)
        next if rate.zero?

        { provider: key, date: Date.parse(date_str), base: currency, quote: "SEK", rate: }
      rescue ArgumentError, TypeError
        nil
      end
    rescue JSON::ParserError
      []
    end
  end
end
