# frozen_string_literal: true

require "json"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banky Foiben'i Madagasikara. Published reference rates in ariary per foreign unit. MID means the interbank FX
    # market; coursMid is the reference, while coursMidMin/Max are daily extremes, not bid/ask prices.
    class BFM < Adapter
      URL = "https://www.banky-foibe.mg/admin/wp-json/bfm/cours_mid_en_ar_filter"
      COVERAGE_START = Date.new(2018, 1, 2)
      CURRENCIES = ["EUR", "USD", "GBP", "CHF", "JPY", "CAD", "DKK", "NOK", "SEK", "DJF", "XDR", "MUR", "ZAR", "AUD",
                    "HKD", "SGD", "NZD", "INR", "CNY",].freeze

      class << self
        def backfill_range = 365
      end

      def fetch(after: nil, upto: nil)
        start_date = after || COVERAGE_START
        end_date = upto || Date.today
        client = http.headers("Origin" => "https://www.banky-foibe.mg", "Referer" => "https://www.banky-foibe.mg/taux-reference")
        records = []
        cursor = start_date
        first = true
        while cursor <= end_date
          last = [cursor + self.class.backfill_range - 1, end_date].min
          CURRENCIES.each do |code|
            sleep(0.2) unless first
            first = false
            form = { dateFilterDebut: cursor.strftime("%Y/%m/%d"), dateFilterFin: last.strftime("%Y/%m/%d"),
                     filterData: code, }
            records.concat(parse(client.post(URL, form:).to_s, code))
          end
          cursor = last + 1
        end
        records.select { |r| r[:date].between?(start_date, end_date) }
      end

      def parse(json, code)
        payload = JSON.parse(json)
        rows = payload.dig("data", "data", "coursMid")
        unless payload.dig("data", "status") == 200 && (rows.is_a?(Hash) || rows == [])
          raise "BFM: missing or invalid reference-rate data for #{code}"
        end
        return [] if rows == []

        rows.filter_map do |date, value|
          rate = BigDecimal(value.to_s.delete(" \u00a0\u202f").tr(",", "."), exception: false)
          next unless rate&.finite? && rate.positive?

          { date: Date.iso8601(date), base: code, quote: "MGA", rate: }
        end
      end
    end
  end
end
