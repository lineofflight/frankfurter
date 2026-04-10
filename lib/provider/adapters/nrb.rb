# frozen_string_literal: true

require "json"
require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Nepal Rastra Bank. Publishes daily buy/sell rates for 22 currencies
    # against NPR. Mid-market rate computed as average of buy and sell.
    # Rates with unit > 1 (e.g. JPY per 10) are normalized by dividing by unit.
    class NRB < Adapter
      BASE_URL = "https://www.nrb.org.np/api/forex/v1/rates"

      class << self
        def backfill_range = 90
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        dataset = []
        page = 1

        loop do
          sleep(0.5) if page > 1
          url = URI(BASE_URL)
          url.query = URI.encode_www_form(
            from: after.to_s,
            to: end_date.to_s,
            page: page,
            per_page: 100,
          )

          response = Net::HTTP.get(url)
          data = JSON.parse(response)
          payload = data.dig("data", "payload") || []
          dataset.concat(parse(payload))

          pagination = data["pagination"] || {}
          break if page >= (pagination["pages"] || 1)

          page += 1
        end

        dataset
      end

      def parse(payload)
        payload = JSON.parse(payload) if payload.is_a?(String)
        return [] unless payload.is_a?(Array)

        payload.flat_map do |day|
          date = Date.parse(day["date"]) rescue (next [])
          rates = day["rates"] || []

          rates.filter_map do |entry|
            iso = entry.dig("currency", "iso3")
            unit = Integer(entry.dig("currency", "unit") || 1)
            buy = entry["buy"]
            sell = entry["sell"]
            next unless buy && sell

            mid = (Float(buy) + Float(sell)) / 2.0
            rate = mid / unit
            next if rate.zero?

            { date:, base: iso, quote: "NPR", rate: }
          end
        end
      end
    end
  end
end
