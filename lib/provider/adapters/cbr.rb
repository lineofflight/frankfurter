# frozen_string_literal: true

require "net/http"
require "ox"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Bank of Russia. Publishes daily rates for ~54 currencies against RUB,
    # plus daily reference prices for gold, silver, platinum and palladium.
    # FX uses XML_daily for the currency list and XML_dynamic for date ranges.
    # Metals come from xml_metall in RUB per gram; values are normalized to
    # per troy ounce here.
    class CBR < Adapter
      DAILY_URL = URI("https://www.cbr.ru/scripts/XML_daily.asp")
      DYNAMIC_URL = URI("https://www.cbr.ru/scripts/XML_dynamic.asp")
      METAL_URL = URI("https://www.cbr.ru/scripts/xml_metall.asp")

      METAL_CODES = {
        "1" => "XAU",
        "2" => "XAG",
        "3" => "XPT",
        "4" => "XPD",
      }.freeze

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        currencies = fetch_currency_list
        fx = currencies.flat_map { |id, code, nominal| fetch_dynamic(id, code, nominal, after, end_date) }
        fx + fetch_metals(after, end_date)
      end

      def parse_metals(xml)
        Ox.load(xml).locate("Metall/Record").filter_map do |row|
          base = METAL_CODES[row[:Code]]
          next unless base

          date = Date.strptime(row[:Date], "%d.%m.%Y")
          next if date.saturday? || date.sunday?

          buy = row.locate("Buy").first&.text
          next unless buy && !buy.empty?

          rate = Float(buy.tr(",", "."), exception: false)
          next unless rate&.positive?

          { date:, base:, quote: "RUB", rate: rate * GRAMS_PER_TROY_OUNCE }
        end
      end

      private

      def fetch_metals(start_date, end_date)
        url = METAL_URL.dup
        url.query = URI.encode_www_form(
          date_req1: start_date.strftime("%d/%m/%Y"),
          date_req2: end_date.strftime("%d/%m/%Y"),
        )

        parse_metals(Net::HTTP.get(url))
      end

      def fetch_currency_list
        doc = Ox.load(Net::HTTP.get(DAILY_URL))
        doc.locate("ValCurs/Valute").filter_map do |v|
          code = v.locate("CharCode").first&.text
          next unless code && !code.empty?

          id = v[:ID]
          nominal = v.locate("Nominal").first&.text.to_i
          [id, code, nominal]
        end
      end

      def fetch_dynamic(valute_id, code, nominal, start_date, end_date)
        url = DYNAMIC_URL.dup
        url.query = URI.encode_www_form(
          date_req1: start_date.strftime("%d/%m/%Y"),
          date_req2: end_date.strftime("%d/%m/%Y"),
          VAL_NM_RQ: valute_id,
        )

        Ox.load(Net::HTTP.get(url)).locate("ValCurs/Record").filter_map do |row|
          date = Date.strptime(row[:Date], "%d.%m.%Y")
          next if date.saturday? || date.sunday?

          rate = extract_rate(row)
          next unless rate

          { date:, base: code, quote: "RUB", rate: }
        end
      end

      def extract_rate(node)
        vunit = node.locate("VunitRate").first
        return Float(vunit.text.tr(",", ".")) if vunit&.text && !vunit.text.empty?

        value = node.locate("Value").first
        return unless value&.text

        nominal = node.locate("Nominal").first&.text.to_i
        Float(value.text.tr(",", ".")) / nominal
      end
    end
  end
end
