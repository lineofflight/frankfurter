# frozen_string_literal: true

require "json"
require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Croatian National Bank. Fetches daily mid exchange rates (EUR-based)
    # via the v3 API. Historical HRK rates (pre-2023) are no longer available
    # as the v2 API was frozen after Croatia adopted the euro.
    class HNB < Adapter
      BASE_URL = "https://api.hnb.hr/tecajn-eur/v3"

      class << self
        def backfill_range = 90
      end

      def fetch(after: nil, upto: nil)
        url = URI(BASE_URL)
        params = {}
        params["datum-primjene-od"] = after.to_s if after
        params["datum-primjene-do"] = (upto || Date.today).to_s if after || upto
        url.query = URI.encode_www_form(params) unless params.empty?

        parse(Net::HTTP.get(url))
      end

      def parse(json)
        rows = JSON.parse(json)
        rows.filter_map do |row|
          date_str = row["datum_primjene"]
          rate_str = row["srednji_tecaj"]
          currency = row["valuta"]
          next unless date_str && rate_str && currency

          rate_value = Float(rate_str.tr(",", "."))
          next if rate_value.zero?

          date = Date.parse(date_str)
          { date:, base: "EUR", quote: currency, rate: rate_value }
        end
      end
    end
  end
end
