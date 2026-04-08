# frozen_string_literal: true

require "net/http"
require "ox"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # National Bank of Kazakhstan. Publishes daily rates for ~38 currencies against KZT.
    # Uses an XML endpoint that supports historical queries via date parameter.
    class NBK < Adapter
      URL = "https://nationalbank.kz/rss/get_rates.cfm"

      class << self
        def backfill_range = 30
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        dataset = []

        first = true
        (after..end_date).each do |date|
          next if date.saturday? || date.sunday?

          sleep(0.2) unless first
          first = false

          dataset.concat(fetch_date(date))
        end

        dataset
      end

      def parse(xml)
        doc = Ox.load(xml)
        date_text = doc.locate("rates/date").first&.text
        return [] unless date_text

        date = Date.strptime(date_text, "%d.%m.%Y")

        doc.locate("rates/item").filter_map do |item|
          code = item.locate("title").first&.text
          next unless code&.match?(/\A[A-Z]{3}\z/)

          rate = item.locate("description").first&.text.to_f
          quant = item.locate("quant").first&.text.to_f
          next if rate.zero? || quant.zero?

          { date:, base: code, quote: "KZT", rate: rate / quant }
        end
      end

      private

      def fetch_date(date)
        url = URI(URL)
        url.query = URI.encode_www_form(fdate: date.strftime("%d.%m.%Y"))
        parse(Net::HTTP.get(url))
      end
    end
  end
end
