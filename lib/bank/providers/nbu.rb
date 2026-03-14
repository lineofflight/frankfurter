# frozen_string_literal: true

require "date"
require "net/http"
require "oj"

require "bank/provider"

module Bank
  module Providers
    class NBU < Provider
      EARLIEST_DATE = Date.new(1999, 1, 4)
      RANGE_URL = URI("https://bank.gov.ua/NBU_Exchange/exchange_site")

      def current
        range(Date.today - 7, Date.today).last(1)
      end

      def ninety_days
        range(Date.today - 120, Date.today)
      end

      def historical
        range(EARLIEST_DATE, Date.today)
      end

      def saved_data
        []
      end

      def supported_currencies
        ["UAH"]
      end

      private

      def range(start_date, end_date)
        rows = fetch_rows(start_date, end_date)
        rows.filter_map do |row|
          rate = extract_rate(row)
          next unless rate

          date = Date.strptime(row.fetch("exchangedate"), "%d.%m.%Y")
          next if date > end_date
          next if date.saturday? || date.sunday?

          {
            date: date,
            rates: { "UAH" => rate },
          }
        end.sort_by { |day| day[:date] }
      end

      def fetch_rows(start_date, end_date)
        url = RANGE_URL.dup
        url.query = URI.encode_www_form(
          start: start_date.strftime("%Y%m%d"),
          end: end_date.strftime("%Y%m%d"),
          valcode: "EUR",
          sort: "exchangedate",
          order: "asc",
          json: "",
        )

        Oj.load(Net::HTTP.get(url))
      end

      def extract_rate(row)
        units = row.fetch("units", 1).to_f
        rate_per_unit = row["rate_per_unit"]
        rate = row["rate"]
        return if units.zero?

        return rate_per_unit.to_f if rate_per_unit
        return if rate.nil?

        rate.to_f / units
      end
    end
  end
end
