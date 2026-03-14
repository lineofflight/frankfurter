# frozen_string_literal: true

require "date"
require "net/http"
require "oj"

require "bank/provider"

module Bank
  module Providers
    class NBRB < Provider
      EARLIEST_DATE = Date.new(1999, 1, 4)
      CURRENT_URL = "https://api.nbrb.by/exrates/rates/EUR?parammode=2"
      DYNAMICS_URL = "https://api.nbrb.by/exrates/rates/dynamics"

      def current
        row = Oj.load(Net::HTTP.get(URI(CURRENT_URL)))

        [
          {
            date: Date.parse(row.fetch("Date")),
            rates: { "BYN" => extract_rate(row) },
          },
        ]
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
        ["BYN"]
      end

      private

      def range(start_date, end_date)
        url = URI("#{DYNAMICS_URL}/#{currency_id}")
        url.query = URI.encode_www_form(
          startDate: start_date.to_s,
          endDate: end_date.to_s,
        )

        Oj.load(Net::HTTP.get(url)).filter_map do |row|
          date = Date.parse(row.fetch("Date"))
          next if date.saturday? || date.sunday?

          {
            date: date,
            rates: { "BYN" => Float(row.fetch("Cur_OfficialRate")) },
          }
        end
      end

      def currency_id
        @currency_id ||= Oj.load(Net::HTTP.get(URI(CURRENT_URL))).fetch("Cur_ID")
      end

      def extract_rate(row)
        Float(row.fetch("Cur_OfficialRate")) / Integer(row.fetch("Cur_Scale"))
      end
    end
  end
end
