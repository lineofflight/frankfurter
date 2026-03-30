# frozen_string_literal: true

require "csv"
require "net/http"

require "providers/base"

module Providers
  # Bank of Israel. Fetches daily representative exchange rates for 14
  # currencies against the Israeli new shekel (ILS) via the SDMX API.
  # Supports date range queries and full historical backfill.
  class BOI < Base
    BASE_URL = "https://edge.boi.gov.il/FusionEdgeServer/sdmx/v2/data/dataflow/BOI.STATISTICS/EXR/1.0/"
    EARLIEST_DATE = Date.new(2000, 1, 1)

    class << self
      def key = "BOI"
      def name = "Bank of Israel"
      def earliest_date = EARLIEST_DATE
    end

    def fetch(since: nil, upto: nil)
      start_date = since || EARLIEST_DATE
      end_date = upto || Date.today

      url = URI(BASE_URL)
      url.query = URI.encode_www_form(
        "c[DATA_TYPE]" => "OF00",
        "startperiod" => start_date.to_s,
        "endperiod" => end_date.to_s,
        "format" => "csv",
      )

      response = Net::HTTP.get(url)
      @dataset = parse(response)
      self
    rescue Net::OpenTimeout, Net::ReadTimeout, Socket::ResolutionError
      @dataset = []
      self
    end

    def parse(csv)
      rows = CSV.parse(csv, headers: true)

      rows.filter_map do |row|
        base = row["BASE_CURRENCY"]
        date_str = row["TIME_PERIOD"]
        rate_str = row["OBS_VALUE"]
        unit_mult = row["UNIT_MULT"]
        next unless base && date_str && rate_str

        rate_value = Float(rate_str)
        # UNIT_MULT is power of 10: 2 means per 100 units, 1 means per 10
        rate_value /= (10**Integer(unit_mult)) if unit_mult && unit_mult != "0"
        next if rate_value.zero?

        date = Date.parse(date_str)
        { provider: key, date:, base:, quote: "ILS", rate: rate_value }
      rescue ArgumentError, TypeError
        nil
      end
    end
  end
end
