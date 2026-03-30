# frozen_string_literal: true

require "json"
require "net/http"

require "providers/base"

module Providers
  # Seðlabanki Íslands (Central Bank of Iceland). Fetches daily official
  # exchange rates for ~20 currencies against the Icelandic króna (ISK)
  # via the public JSON API. Rates are published on Icelandic business days.
  class SBI < Base
    BASE_URL = "https://api.sedlabanki.is/ExchangeRate/GetExchangeRatesByDate"
    EARLIEST_DATE = Date.new(2000, 1, 3)

    class << self
      def key = "SBI"
      def name = "Seðlabanki Íslands"
      def earliest_date = EARLIEST_DATE

      def backfill(range: 30)
        super
      end
    end

    def fetch(since: nil, upto: nil)
      start_date = since || EARLIEST_DATE
      end_date = upto || Date.today

      @dataset = []
      (start_date..end_date).each do |date|
        next if date.saturday? || date.sunday?

        @dataset.concat(fetch_date(date))
      end

      self
    rescue Net::OpenTimeout, Net::ReadTimeout
      @dataset = []
      self
    end

    def parse(json, date:)
      data = json.is_a?(String) ? JSON.parse(json) : json
      return [] unless data.is_a?(Array)

      data.filter_map do |row|
        iso = row["shortName"]
        next unless iso&.match?(/\A[A-Z]{3}\z/)

        mid_rate = row["midRate"]
        next if mid_rate.nil?

        units = (row["units"] || 1).to_f
        rate_value = mid_rate.to_f / units
        next if rate_value.zero?

        { provider: key, date:, base: iso, quote: "ISK", rate: rate_value }
      rescue ArgumentError, TypeError
        nil
      end
    end

    private

    def fetch_date(date)
      url = URI(BASE_URL)
      url.query = URI.encode_www_form(date: date.to_s, lang: "en")
      response = Net::HTTP.get(url)
      parse(response, date: date)
    rescue JSON::ParserError
      []
    end
  end
end
