# frozen_string_literal: true

require "json"
require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Central Bank of the Republic of China (Taiwan). Publishes daily interbank
    # spot rates for 15+ currencies against the US dollar. Rates are captured at
    # 16:00 Taipei time (08:00 UTC). The API returns the full historical dataset
    # (no server-side date filtering); client-side filtering is applied.
    # Historical data available from 1993-01-05.
    class CBC < Adapter
      API_URL = "https://cpx.cbc.gov.tw/API/DataAPI/Get?FileName=BP01D01"
      # Column index => [quote, base]
      # Most rates are quoted as foreign currency per 1 USD (quote=X, base=USD).
      # GBP, AUD, EUR are quoted as USD per 1 unit (quote=USD, base=X).
      # Discontinued currencies (DEM, FRF, NLG) are excluded.
      COLUMNS = {
        1 => ["TWD", "USD"],
        2 => ["JPY", "USD"],
        3 => ["USD", "GBP"],
        4 => ["HKD", "USD"],
        5 => ["KRW", "USD"],
        6 => ["CAD", "USD"],
        7 => ["SGD", "USD"],
        8 => ["CNY", "USD"],
        9 => ["USD", "AUD"],
        10 => ["IDR", "USD"],
        11 => ["THB", "USD"],
        12 => ["MYR", "USD"],
        13 => ["PHP", "USD"],
        14 => ["USD", "EUR"],
        18 => ["VND", "USD"],
      }.freeze

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today

        response = Net::HTTP.get(URI(API_URL))
        data = JSON.parse(response)
        rows = data.dig("data", "dataSets") || []

        parse(rows, after, end_date)
      end

      def parse(rows, start_date = nil, end_date = nil)
        rows.each_with_object([]) do |row, result|
          next unless row.is_a?(Array) && row.length > 1

          date = Date.strptime(row[0], "%Y%m%d")
          next if start_date && date < start_date
          next if end_date && date > end_date

          COLUMNS.each do |index, (quote, base)|
            value = row[index]
            next if value.nil? || value == "-"

            rate = Float(value)
            next if rate.zero?

            result << { date:, base:, quote:, rate: }
          end
        end
      end
    end
  end
end
