# frozen_string_literal: true

require "json"
require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Central Bank of the Republic of Turkey.
    # Uses EVDS3 bulk API with buying (A) and selling (S) rates in TRY.
    # Each currency's mid-market rate is derived from the average of buy/sell.
    # Requires TCMB_API_KEY environment variable.
    class TCMB < Adapter
      EVDS_URL = "https://evds3.tcmb.gov.tr/igmevdsms-dis"
      # Buy/sell rates in TRY for each currency. Hardcoded because the EVDS3 catalog API doesn't expose
      # a clean list and the series rarely change.
      # Browse: https://evds3.tcmb.gov.tr > Exchange Rates > Indicative Exchange Rates
      CURRENCIES = [
        "AED",
        "AUD",
        "AZN",
        "CAD",
        "CHF",
        "CNY",
        "DKK",
        "EUR",
        "GBP",
        "JPY",
        "KRW",
        "KWD",
        "KZT",
        "NOK",
        "PKR",
        "QAR",
        "RON",
        "RUB",
        "SAR",
        "SEK",
        "USD",
      ].freeze

      SERIES = CURRENCIES.flat_map do |c|
        [["#{c}_BUY", "TP.DK.#{c}.A.YTL"], ["#{c}_SELL", "TP.DK.#{c}.S.YTL"]]
      end.to_h.freeze

      COLUMNS = SERIES.to_h { |code, series| [series.tr(".", "_"), code] }.freeze

      class << self
        def api_key = ENV["TCMB_API_KEY"] || raise(ApiKeyMissing)
        def backfill_range = 730
      end

      def fetch(after: nil, upto: nil)
        start_date = after || Date.new(2012, 1, 2)
        end_date = upto || Date.today

        url = URI("#{EVDS_URL}/series=#{SERIES.values.join("-")}" \
          "&startDate=#{start_date.strftime("%d-%m-%Y")}" \
          "&endDate=#{end_date.strftime("%d-%m-%Y")}" \
          "&type=json&frequency=1")

        response = Net::HTTP.get(url, "key" => self.class.api_key)
        data = JSON.parse(response)
        items = data["items"] || []

        items.flat_map do |item|
          date = Date.strptime(item["Tarih"], "%d-%m-%Y")
          raw = {}

          COLUMNS.each do |column, code|
            value = item[column]
            next if value.nil?

            raw[code] = Float(value)
          end

          # Mid of buying and selling for each currency → X→TRY rate
          # JPY is quoted per 100 units in TCMB data (confirmed via series metadata)
          CURRENCIES.filter_map do |currency|
            buy = raw["#{currency}_BUY"]
            sell = raw["#{currency}_SELL"]
            next unless buy && sell

            rate = (buy + sell) / 2
            rate /= 100.0 if currency == "JPY"

            { date:, base: currency, quote: "TRY", rate: rate.round(4) }
          end
        end
      end
    end
  end
end
