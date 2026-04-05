# frozen_string_literal: true

require "json"
require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Financial Benchmarks India (FBIL). Publishes daily reference exchange rates
    # for major currencies against the Indian rupee via a public JSON API.
    # Rates are expressed as INR per N units of foreign currency, where N is
    # extracted from the subProdName field (e.g. "INR / 100 JPY" means per 100).
    # Historical data available from 2018-07-10.
    class FBIL < Adapter
      BASE_URL = "https://www.fbil.org.in/wasdm/refrates/fetchfiltered"
      def fetch(after: nil, upto: nil)
        url = URI(BASE_URL)
        url.query = URI.encode_www_form(
          "fromDate" => after.to_s,
          "toDate" => (upto || Date.today).to_s,
          "authenticated" => "false",
        )

        response = Net::HTTP.get(url)
        @dataset = parse(response)
        @dataset
      end

      def parse(json)
        data = json.is_a?(String) ? JSON.parse(json) : json
        return [] unless data.is_a?(Array)

        data.filter_map do |record|
          sub_prod = record["subProdName"]
          date_str = record["processRunDate"]
          rate_value = record["rate"]
          next unless sub_prod && date_str && rate_value.is_a?(Numeric)

          match = sub_prod.match(%r{INR / (\d+) ([A-Z]{3})})
          next unless match

          units = match[1].to_i
          currency = match[2]
          next if units.zero?

          adjusted_rate = rate_value / units.to_f
          next if adjusted_rate.zero?

          date = Date.parse(date_str)
          { date:, base: currency, quote: "INR", rate: adjusted_rate }
        end
      end
    end
  end
end
