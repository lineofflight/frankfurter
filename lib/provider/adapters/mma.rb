# frozen_string_literal: true

require "date"
require "json"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Maldives Monetary Authority. Publishes a single rolling JSON file with
    # the daily reference rate of the rufiyaa against the US dollar. The
    # rufiyaa is USD-pegged within a crawling band, so MMA only publishes the
    # one pair (USD/MVR). The file always returns the full history, so we
    # filter client-side by `after`. The feed occasionally emits duplicate
    # entries for the same date (e.g. "08/09 February 2021"); we keep the
    # first occurrence per (date, base, quote) so upserts stay deterministic.
    class MMA < Adapter
      BASE_URL = "https://www.mma.gov.mv/JSON/referencerates.json"

      def fetch(after: nil, upto: nil)
        response = http.get(BASE_URL, params: { v: (upto || Date.today).strftime("%Y%m%d") }).to_s

        records = parse(response)
        records = records.select { |r| r[:date] >= after } if after
        records = records.select { |r| r[:date] <= upto } if upto
        records
      end

      def parse(json)
        records = json.is_a?(String) ? JSON.parse(json) : json
        return [] unless records.is_a?(Array)

        seen = {}
        records.each do |record|
          date_str = record["Date"]
          rate_raw = record["Rate"]
          next if date_str.nil? || rate_raw.nil?

          rate_value = Float(rate_raw)
          next if rate_value.zero?

          date = Date.parse(date_str)
          key = [date, "USD", "MVR"]
          next if seen.key?(key)

          seen[key] = { date:, base: "USD", quote: "MVR", rate: rate_value }
        end
        seen.values
      end
    end
  end
end
