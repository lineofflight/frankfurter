# frozen_string_literal: true

require "net/http"
require "ox"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banca Națională a Moldovei (BNM). Publishes daily rates for 30+ currencies against MDL.
    # Date-parameterized XML endpoint, one request per day.
    class BNM < Adapter
      URL = "https://www.bnm.md/en/official_exchange_rates"

      class << self
        def backfill_range = 30
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        dataset = []

        first = true
        (after..end_date).each do |date|
          next if date.saturday? || date.sunday?

          sleep(0.5) unless first
          first = false

          dataset.concat(fetch_date(date))
        end

        dataset
      end

      def parse(xml)
        doc = Ox.load(xml)
        val_curs = doc.locate("ValCurs").first
        return [] unless val_curs

        date_str = val_curs[:Date]
        return [] unless date_str

        date = Date.strptime(date_str, "%d.%m.%Y")

        val_curs.locate("Valute").filter_map do |v|
          code = v.locate("CharCode").first&.text
          next unless code&.match?(/\A[A-Z]{3}\z/)

          nominal = v.locate("Nominal").first&.text.to_f
          value = v.locate("Value").first&.text.to_f
          next if value.zero? || nominal.zero?

          { date:, base: code, quote: "MDL", rate: value / nominal }
        end
      end

      private

      def fetch_date(date)
        url = URI(URL)
        url.query = URI.encode_www_form(get_xml: 1, date: date.strftime("%d.%m.%Y"))
        parse(Net::HTTP.get(url))
      end
    end
  end
end
