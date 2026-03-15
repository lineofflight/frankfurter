# frozen_string_literal: true

require "date"
require "net/http"
require "ox"

require "bank/provider"

module Bank
  module Providers
    class CBR < Provider
      EARLIEST_DATE = Date.new(1999, 1, 4)
      DYNAMIC_URL = URI("https://www.cbr.ru/scripts/XML_dynamic.asp")
      EUR_ID = "R01239"

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
        ["RUB"]
      end

      private

      def range(start_date, end_date)
        url = DYNAMIC_URL.dup
        url.query = URI.encode_www_form(
          date_req1: start_date.strftime("%d/%m/%Y"),
          date_req2: end_date.strftime("%d/%m/%Y"),
          VAL_NM_RQ: EUR_ID,
        )

        Ox.load(Net::HTTP.get(url)).locate("ValCurs/Record").filter_map do |row|
          date = Date.strptime(row[:Date], "%d.%m.%Y")
          next if date.saturday? || date.sunday?

          {
            date: date,
            rates: { "RUB" => extract_rate(row) },
          }
        end
      end

      def extract_rate(node)
        value = node.locate("VunitRate").first || node.locate("Value").first
        nominal = node.locate("Nominal").first
        rate = parse_decimal(value.text)
        return rate unless nominal

        rate / nominal.text.to_i
      end

      def parse_decimal(value)
        Float(value.tr(",", "."))
      end
    end
  end
end
