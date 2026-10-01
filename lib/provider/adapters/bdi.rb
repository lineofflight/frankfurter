# frozen_string_literal: true

require "csv"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banca d'Italia. Publishes daily exchange rates for 150+ currencies against the euro via the "terze valute" (third
    # currencies) portal. Uses the dailyRates endpoint with currencyIsoCode=EUR to get all currencies quoted against EUR
    # for a single date.
    class BDI < Adapter
      URL = "https://tassidicambio.bancaditalia.it/terzevalute-wf-web/rest/v1.0/dailyRates"

      # BDI labels its old-afghani quotes AFN. Until 2004-03-31 they hold the frozen official rate of 4750 AFA to the
      # dollar (5806.4 per euro with the dollar at 1.2224), long past the October 2002 redenomination. On 2004-04-01
      # they switch to 47.5 new afghani to the dollar (58.52 per euro at 1.232).
      PREDECESSORS = { "AFN" => ["AFA", Date.new(2004, 4, 1)] }.freeze

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

          rate_str = row["Rate"]
          next if rate_str.nil? || rate_str.strip == "N.A."

          rate_value = Float(rate_str)
          next if rate_value.zero?

          date_str = row["Reference date (CET)"]
          next unless date_str

          date = Date.parse(date_str)

          { date:, base: "EUR", quote: historical_code(code, date), rate: rate_value }
        end
      end

      private

      def fetch_date(date)
        response = http.get(URL, params: {
          referenceDate: date.strftime("%Y-%m-%d"),
          currencyIsoCode: "EUR",
          lang: "en",
        },).to_s
        parse(response)
      end
    end
  end
end
