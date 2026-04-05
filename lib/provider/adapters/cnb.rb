# frozen_string_literal: true

require "net/http"
require "oj"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Czech National Bank. Publishes daily exchange rates for 30 currencies
    # against the Czech koruna (CZK) via a REST JSON API.
    class CNB < Adapter
      URL = "https://api.cnb.cz/cnbapi/exrates/daily-year"
      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        dataset = []

        (after.year..end_date.year).each do |year|
          dataset.concat(fetch_year(year))
        end

        dataset.select! { |r| r[:date].between?(after, end_date) }
      end

      def parse(json)
        data = json.is_a?(String) ? Oj.load(json, mode: :strict) : json
        rates = data.is_a?(Hash) ? data["rates"] : nil
        return [] unless rates.is_a?(Array) && !rates.empty?

        rates.filter_map do |r|
          code = r["currencyCode"]
          next unless code&.match?(/\A[A-Z]{3}\z/)

          amount = r["amount"].to_f
          rate = r["rate"].to_f
          next if rate.zero? || amount.zero?

          date = Date.parse(r["validFor"])
          { date:, base: code, quote: "CZK", rate: rate / amount }
        end
      end

      private

      def fetch_year(year)
        url = URI(URL)
        url.query = URI.encode_www_form(year: year, lang: "EN")
        response = Net::HTTP.get(url)
        parse(response)
      end
    end
  end
end
