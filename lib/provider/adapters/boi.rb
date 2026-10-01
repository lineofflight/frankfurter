# frozen_string_literal: true

require "csv"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Bank of Israel. Fetches daily representative exchange rates for 14 currencies against the Israeli new shekel (ILS)
    # via the SDMX API. Supports date range queries and full historical backfill.
    class BOI < Adapter
      BASE_URL = "https://edge.boi.gov.il/FusionEdgeServer/sdmx/v2/data/dataflow/BOI.STATISTICS/EXR/1.0/"

      # The pre-euro series to January 2002 quote some currencies per 10, 100 or 1000 units while UNIT_MULT says units:
      # on 2000-01-03, with EUR at 4.1603 ILS, ATS reads 3.0234 and ESP 2.5004, ten and a hundred times the euro
      # conversion rates, and every day of the series holds the same multiple.
      NOMINAL_OVERRIDES = { "ATS" => 10, "BEL" => 10, "ESP" => 100, "ITL" => 1000 }.freeze

      # BOI's codelist names BEL the financial franc, abolished in 1990, but its 1999-2002 series tracks 10 BEF at the
      # euro conversion rate.
      ALIASES = { "BEL" => "BEF" }.freeze

      def fetch(after: nil, upto: nil)
        response = http.get(BASE_URL, params: {
          "c[DATA_TYPE]" => "OF00",
          "startperiod" => after.to_s,
          "endperiod" => (upto || Date.today).to_s,
          "format" => "csv",
        },).to_s

        parse(response)
      end

      def parse(csv)
        rows = CSV.parse(csv, headers: true)

        rows.filter_map do |row|
          base = row["BASE_CURRENCY"]
          date_str = row["TIME_PERIOD"]
          rate_str = row["OBS_VALUE"]
          unit_mult = row["UNIT_MULT"]
          next unless base && date_str && rate_str
          # Aggregates such as CBK_L, the currency basket, share the flow with currencies.
          next unless base.match?(/\A[A-Z]{3}\z/)

          rate_value = Float(rate_str)
          # UNIT_MULT is power of 10: 2 means per 100 units, 1 means per 10
          rate_value /= (10**Integer(unit_mult)) if unit_mult && unit_mult != "0"
          rate_value /= NOMINAL_OVERRIDES.fetch(base, 1)
          next if rate_value.zero?

          date = Date.parse(date_str)
          { date:, base: ALIASES.fetch(base, base), quote: "ILS", rate: rate_value }
        end
      end
    end
  end
end
