# frozen_string_literal: true

require "net/http"
require "oj"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # National Bank of Georgia. Publishes daily rates for 40+ currencies against GEL.
    class NBG < Adapter
      URL = "https://nbg.gov.ge/gw/api/ct/monetarypolicy/currencies/"
      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        @dataset = []

        first = true
        (after..end_date).each do |date|
          next if date.saturday? || date.sunday?

          sleep(0.2) unless first
          first = false

          @dataset.concat(fetch_date(date))
        end

        @dataset
      end

      def parse(json)
        data = json.is_a?(String) ? Oj.load(json, mode: :strict) : json
        return [] unless data.is_a?(Array) && data.first

        entry = data.first
        date = Date.parse(entry["date"])
        currencies = entry["currencies"] || []

        currencies.filter_map do |cur|
          code = cur["code"]
          next unless code&.match?(/\A[A-Z]{3}\z/)

          quantity = cur["quantity"].to_f
          rate = cur["rate"].to_f
          next if rate.zero? || quantity.zero?

          { date:, base: code, quote: "GEL", rate: rate / quantity }
        end
      end

      private

      def fetch_date(date)
        url = URI(URL)
        url.query = URI.encode_www_form(date: date.strftime("%Y-%m-%d"))
        response = Net::HTTP.get(url)
        parse(response)
      end
    end
  end
end
