# frozen_string_literal: true

require "csv"
require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Bank of England (BOE). Fetches daily spot exchange rates for 26 currencies
    # against the British pound from the Statistical Interactive Database.
    # The CSV API returns pivoted data with series codes as column headers.
    # Historical data available from 2000-01-04.
    class BOE < Adapter
      BASE_URL = "https://www.bankofengland.co.uk/boeapps/database/_iadb-fromshowcolumns.asp"
      # BOE series code => ISO 4217 currency code
      # Rates are "foreign currency per 1 GBP"
      SERIES = {
        "XUDLUSS" => "USD",
        "XUDLERS" => "EUR",
        "XUDLJYS" => "JPY",
        "XUDLCDS" => "CAD",
        "XUDLSFS" => "CHF",
        "XUDLADS" => "AUD",
        "XUDLNDS" => "NZD",
        "XUDLNKS" => "NOK",
        "XUDLSKS" => "SEK",
        "XUDLDKS" => "DKK",
        "XUDLHDS" => "HKD",
        "XUDLSGS" => "SGD",
        "XUDLSRS" => "SAR",
        "XUDLZRS" => "ZAR",
        "XUDLTWS" => "TWD",
        "XUDLBK25" => "CZK",
        "XUDLBK33" => "HUF",
        "XUDLBK47" => "PLN",
        "XUDLBK78" => "ILS",
        "XUDLBK83" => "MYR",
        "XUDLBK87" => "THB",
        "XUDLBK89" => "CNY",
        "XUDLBK93" => "KRW",
        "XUDLBK95" => "TRY",
        "XUDLBK97" => "INR",
        "XUDLZOS4" => "RON",
      }.freeze

      def fetch(after: nil, upto: nil)
        url = URI(BASE_URL)
        url.query = URI.encode_www_form(
          "csv.x" => "yes",
          "SeriesCodes" => SERIES.keys.join(","),
          "UsingCodes" => "Y",
          "CSVF" => "TN",
          "Datefrom" => after.strftime("%d/%b/%Y"),
          "Dateto" => (upto || Date.today).strftime("%d/%b/%Y"),
        )

        response = Net::HTTP.get(url)
        @dataset = parse(response)
      end

      def parse(csv)
        rows = CSV.parse(csv, headers: true)

        rows.flat_map do |row|
          date_str = row["DATE"]
          next unless date_str

          date = Date.parse(date_str)

          SERIES.filter_map do |series_code, currency|
            rate_str = row[series_code]
            next if rate_str.nil? || rate_str.strip.empty?

            rate_value = Float(rate_str)
            next if rate_value.zero?

            { date:, base: "GBP", quote: currency, rate: rate_value }
          end
        end.compact
      end
    end
  end
end
