# frozen_string_literal: true

require "oj"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Coin Metrics (CM). Its Community Network Data API publishes daily end-of-day (00:00 UTC) USD reference rates
    # (`PriceUSD`) for major crypto assets, reaching Bitcoin's spot trading inception on 2010-07-18.
    #
    # License (CC BY-NC 4.0): https://raw.githubusercontent.com/coinmetrics/data/master/LICENSE
    class CM < Adapter
      API_URL = "https://community-api.coinmetrics.io/v4/timeseries/asset-metrics"
      ASSETS = [
        "ada", "bch", "btc", "dot", "etc", "eth", "fil", "ltc",
        "pol", "sol", "sui", "uni", "xlm", "xrp", "xtz", "zec",
      ].freeze
      COVERAGE_START = Date.new(2010, 7, 18)

      class << self
        def backfill_range = 365
      end

      def fetch(after: nil, upto: nil)
        start_date = [after || COVERAGE_START, COVERAGE_START].max
        end_date = [upto || Date.today, Date.today].min
        return [] if start_date > end_date

        fetch_range(start_date, end_date)
      end

      def parse(json)
        body = Oj.load(json, mode: :strict)
        entries = body["data"]
        raise "CM: missing data array in response" unless entries.is_a?(Array)

        entries.filter_map do |entry|
          value = entry["PriceUSD"]
          next unless value

          rate = Float(value, exception: false)
          next unless rate&.positive?

          date = Date.iso8601(entry.fetch("time")[0, 10])
          base = entry.fetch("asset").upcase
          { date:, base:, quote: "USD", rate: }
        end
      end

      private

      def fetch_range(start_date, end_date)
        dataset = []
        url = API_URL
        params = {
          assets: ASSETS.join(","),
          metrics: "PriceUSD",
          start_time: start_date.iso8601,
          end_time: end_date.iso8601,
          page_size: 10_000,
          paging_from: "start",
        }

        while url
          json = http.headers("Accept" => "application/json").get(url, params:).to_s
          dataset.concat(parse(json))
          url = Oj.load(json, mode: :strict)["next_page_url"]
          params = nil
        end

        dataset
      end
    end
  end
end
