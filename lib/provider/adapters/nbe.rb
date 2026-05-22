# frozen_string_literal: true

require "net/http"
require "oj"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # National Bank of Ethiopia. Publishes daily reference exchange rates for 18 currencies
    # against the Ethiopian birr (ETB) on weekdays. The API returns buying, selling, and
    # weighted_average per currency for a given date. We use weighted_average as the mid.
    #
    # XDR (SDR composite unit) is excluded. Coverage starts 2024-10-01 to skip the
    # July to September 2024 float-transition gap.
    class NBE < Adapter
      URL = "https://api.nbe.gov.et/api/filter-exchange-rates"
      EXCLUDED_CURRENCIES = ["XDR"].freeze

      class << self
        def backfill_range = 30
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        dataset = []

        first = true
        (after..end_date).each do |date|
          next if date.saturday? || date.sunday?

          sleep(0.2) unless first
          first = false

          dataset.concat(fetch_date(date))
        end

        dataset
      end

      def parse(json)
        data = json.is_a?(String) ? Oj.load(json, mode: :strict) : json
        entries = data.is_a?(Hash) ? data["data"] : nil
        return [] unless entries.is_a?(Array)

        entries.filter_map do |entry|
          code = entry.dig("currency", "code")
          next unless code&.match?(/\A[A-Z]{3}\z/)
          next if EXCLUDED_CURRENCIES.include?(code)

          rate = entry["weighted_average"].to_f
          next if rate.zero?

          date = Date.parse(entry["date"])

          { date:, base: code, quote: "ETB", rate: }
        end
      end

      private

      def fetch_date(date)
        url = URI(URL)
        url.query = URI.encode_www_form(date: date.strftime("%Y-%m-%d"))
        response = Net::HTTP.get(url)
        parse(response)
      end
    end
  end
end
