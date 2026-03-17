# frozen_string_literal: true

require "net/http"
require "ox"

require "providers/base"

module Providers
  # Bank of Russia. Publishes daily rates for ~54 currencies against RUB.
  # Uses XML_daily for current (all currencies, single date) and
  # XML_dynamic for historical (per currency, full date range).
  class CBR < Base
    DAILY_URL = URI("https://www.cbr.ru/scripts/XML_daily.asp")
    DYNAMIC_URL = URI("https://www.cbr.ru/scripts/XML_dynamic.asp")
    EARLIEST_DATE = Date.new(1999, 1, 4)

    def key = "CBR"
    def name = "Bank of Russia"
    def base = "RUB"

    def current
      @dataset = fetch_daily(Date.today)
      self
    end

    def historical(start_date: EARLIEST_DATE, end_date: Date.today)
      start_date = Date.parse(start_date.to_s)
      end_date = Date.parse(end_date.to_s)
      currencies = fetch_currency_list
      @dataset = currencies.flat_map do |id, code, nominal|
        fetch_dynamic(id, code, nominal, start_date, end_date)
      end
      self
    end

    private

    def fetch_daily(date)
      url = DAILY_URL.dup
      url.query = URI.encode_www_form(date_req: date.strftime("%d/%m/%Y"))

      doc = Ox.load(Net::HTTP.get(url))
      parse_date = Date.strptime(doc.locate("ValCurs").first[:Date], "%d.%m.%Y")

      doc.locate("ValCurs/Valute").filter_map do |v|
        quote = v.locate("CharCode").first&.text
        next unless quote && !quote.empty?
        next if ["XAU", "XAG", "XPT", "XPD", "XDR"].include?(quote)

        rate = extract_rate(v)
        next unless rate

        { provider: key, date: parse_date, base:, quote:, rate: }
      end
    end

    def fetch_currency_list
      doc = Ox.load(Net::HTTP.get(DAILY_URL))
      doc.locate("ValCurs/Valute").filter_map do |v|
        code = v.locate("CharCode").first&.text
        next unless code && !code.empty?
        next if ["XAU", "XAG", "XPT", "XPD", "XDR"].include?(code)

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

        { provider: key, date:, base:, quote: code, rate: }
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
