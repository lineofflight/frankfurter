# frozen_string_literal: true

require "csv"
require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banca d'Italia. Publishes daily exchange rates for 150+ currencies against the euro via the "terze valute"
    # (third currencies) portal. Uses the dailyRates endpoint with currencyIsoCode=EUR to get all currencies quoted
    # against EUR for a single date.
    class BDI < Adapter
      URL = "https://tassidicambio.bancaditalia.it/terzevalute-wf-web/rest/v1.0/dailyRates"

      # The KPW/EUR rates the API returns (~2.5-3.2) are useless. Exclude to polluting data.
      EXCLUDED_CURRENCIES = ["KPW"].freeze

      class << self
        def backfill_range = 30
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        dataset = []

        first = true
        (after..end_date).each do |date|
          next if date.saturday? || date.sunday?

          sleep(0.3) unless first
          first = false

          dataset.concat(fetch_date(date))
        end

        dataset
      end

      def parse(csv)
        rows = CSV.parse(csv, headers: true)

        rows.filter_map do |row|
          code = row["ISO Code"]
          next unless code&.match?(/\A[A-Z]{3}\z/)
          next if EXCLUDED_CURRENCIES.include?(code)

          rate_str = row["Rate"]
          next if rate_str.nil? || rate_str.strip == "N.A."

          rate_value = Float(rate_str)
          next if rate_value.zero?

          date_str = row["Reference date (CET)"]
          next unless date_str

          date = Date.parse(date_str)

          { date:, base: "EUR", quote: code, rate: rate_value }
        end
      end

      private

      def fetch_date(date)
        url = URI(URL)
        url.query = URI.encode_www_form(
          referenceDate: date.strftime("%Y-%m-%d"),
          currencyIsoCode: "EUR",
          lang: "en",
        )
        response = Net::HTTP.get(url)
        parse(response)
      end
    end
  end
end
