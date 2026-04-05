# frozen_string_literal: true

require "net/http"
require "oj"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # National Bank of Ukraine. Publishes daily rates for ~45 currencies against UAH.
    class NBU < Adapter
      URL = URI("https://bank.gov.ua/NBU_Exchange/exchange_site")
      def fetch(after: nil, upto: nil)
        @dataset = fetch_rates(after, Date.today)
        @dataset
      end

      def parse(json)
        data = json.is_a?(String) ? Oj.load(json, mode: :strict) : json

        data.filter_map do |row|
          date = Date.strptime(row.fetch("exchangedate"), "%d.%m.%Y")
          next if date.saturday? || date.sunday?

          iso = row.fetch("cc")
          next unless iso.match?(/\A[A-Z]{3}\z/)

          units = row.fetch("units", 1).to_f
          rate = row.fetch("rate").to_f
          next if rate.zero? || units.zero?

          { date:, base: iso, quote: "UAH", rate: rate / units }
        end
      end

      private

      def fetch_rates(start_date, end_date)
        url = URL.dup
        url.query = URI.encode_www_form(
          start: start_date.strftime("%Y%m%d"),
          end: end_date.strftime("%Y%m%d"),
          sort: "exchangedate",
          order: "asc",
          json: "",
        )

        parse(Net::HTTP.get(url))
      end
    end
  end
end
