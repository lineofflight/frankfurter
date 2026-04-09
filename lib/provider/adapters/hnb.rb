# frozen_string_literal: true

require "json"
require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Croatian National Bank. Fetches daily mid exchange rates via two APIs:
    # v2 for pre-EUR HRK-based rates (through 2022-12-31) and v3 for post-EUR
    # EUR-based rates (from 2023-01-02). Supports date range queries.
    class HNB < Adapter
      V2_URL = "https://api.hnb.hr/tecajn/v2"
      V3_URL = "https://api.hnb.hr/tecajn-eur/v3"
      EUR_CUTOVER = Date.new(2023, 1, 1)

      class << self
        def backfill_range = 90
      end

      def fetch(after: nil, upto: nil)
        if after && after >= EUR_CUTOVER
          fetch_v3(after:, upto:)
        elsif upto && upto < EUR_CUTOVER
          fetch_v2(after:, upto:)
        else
          fetch_v2(after:, upto: EUR_CUTOVER - 1) + fetch_v3(after: EUR_CUTOVER, upto:)
        end
      end

      def parse_v2(json)
        rows = JSON.parse(json)
        rows.filter_map do |row|
          date_str = row["datum"]
          rate_str = row["srednji_tecaj"]
          currency = row["valuta"]
          unit = row["jedinica"]
          next unless date_str && rate_str && currency

          rate_value = Float(rate_str.tr(",", "."))
          rate_value /= Integer(unit) if unit && unit != 1
          next if rate_value.zero?

          date = Date.parse(date_str)
          { date:, base: currency, quote: "HRK", rate: rate_value }
        end
      end

      def parse_v3(json)
        rows = JSON.parse(json)
        rows.filter_map do |row|
          date_str = row["datum_primjene"]
          rate_str = row["srednji_tecaj"]
          currency = row["valuta"]
          next unless date_str && rate_str && currency

          rate_value = Float(rate_str.tr(",", "."))
          next if rate_value.zero?

          date = Date.parse(date_str)
          { date:, base: currency, quote: "EUR", rate: rate_value }
        end
      end

      private

      def fetch_v2(after: nil, upto: nil)
        url = URI(V2_URL)
        params = {}
        params["datum-od"] = after.to_s if after
        params["datum-do"] = (upto || Date.today).to_s if after || upto
        url.query = URI.encode_www_form(params) unless params.empty?

        response = Net::HTTP.get(url)
        parse_v2(response)
      end

      def fetch_v3(after: nil, upto: nil)
        url = URI(V3_URL)
        params = {}
        params["datum-primjene-od"] = after.to_s if after
        params["datum-primjene-do"] = (upto || Date.today).to_s if after || upto
        url.query = URI.encode_www_form(params) unless params.empty?

        response = Net::HTTP.get(url)
        parse_v3(response)
      end
    end
  end
end
