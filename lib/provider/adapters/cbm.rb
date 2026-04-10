# frozen_string_literal: true

require "json"
require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Central Bank of Myanmar. Publishes daily reference exchange rates for 37
    # currencies against the Myanmar kyat (MMK) via a public JSON API. Rates are
    # expressed as MMK per 1 unit of foreign currency. No historical API is
    # available — only the latest rates are fetched.
    class CBM < Adapter
      BASE_URL = "https://forex.cbm.gov.mm/api/latest"

      def fetch(after: nil, upto: nil)
        response = Net::HTTP.get(URI(BASE_URL))
        parse(response)
      end

      def parse(json)
        data = json.is_a?(String) ? JSON.parse(json) : json
        timestamp = data["timestamp"]
        rates = data["rates"]
        return [] unless timestamp && rates

        date = Time.at(Integer(timestamp)).utc.to_date

        rates.filter_map do |iso, value_str|
          next if iso == "MMK"

          rate_value = Float(value_str)
          next if rate_value.zero?

          { date:, base: iso, quote: "MMK", rate: rate_value }
        end
      end
    end
  end
end
