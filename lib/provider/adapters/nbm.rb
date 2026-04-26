# frozen_string_literal: true

require "net/http"
require "ox"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # National Bank of Moldova (Banca Națională a Moldovei). Publishes daily rates for
    # 30+ currencies against MDL, plus daily reference prices for gold and silver.
    # Date-parameterized XML endpoints, one request per day. Metals come from
    # official_metal_rates in MDL per gram; values are normalized to per troy ounce here.
    class NBM < Adapter
      FX_URL = "https://www.bnm.md/en/official_exchange_rates"
      METAL_URL = "https://www.bnm.md/en/official_metal_rates"

      METAL_CODES = ["XAU", "XAG"].freeze

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

          sleep(0.5)
          dataset.concat(fetch_metals_date(date))
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

      def parse_metals(xml)
        doc = Ox.load(xml)
        metal_price = doc.locate("MetalPrice").first
        return [] unless metal_price

        date_str = metal_price[:Date]
        return [] unless date_str

        date = Date.strptime(date_str, "%d.%m.%Y")
        return [] if date.saturday? || date.sunday?

        metal_price.locate("Metal").filter_map do |m|
          code = m.locate("CharCode").first&.text
          next unless METAL_CODES.include?(code)

          nominal = m.locate("Nominal").first&.text.to_f
          value = m.locate("Value").first&.text.to_f
          next if value.zero? || nominal.zero?

          { date:, base: code, quote: "MDL", rate: (value / nominal) * GRAMS_PER_TROY_OUNCE }
        end
      end

      private

      def fetch_date(date)
        url = URI(FX_URL)
        url.query = URI.encode_www_form(get_xml: 1, date: date.strftime("%d.%m.%Y"))
        parse(Net::HTTP.get(url))
      end

      def fetch_metals_date(date)
        url = URI(METAL_URL)
        url.query = URI.encode_www_form(get_xml: 1, date: date.strftime("%d.%m.%Y"))
        parse_metals(Net::HTTP.get(url))
      end
    end
  end
end
