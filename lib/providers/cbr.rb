# frozen_string_literal: true

require "net/http"
require "ox"

require "providers/base"

module Providers
  # Bank of Russia. Publishes daily rates for ~54 currencies against RUB.
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
      @dataset = fetch_daily(Date.parse(end_date.to_s))
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

        { provider: key, date: parse_date, base:, quote:, rate: extract_rate(v) }
      end
    end

    def extract_rate(node)
      value = node.locate("VunitRate").first || node.locate("Value").first
      nominal = node.locate("Nominal").first
      rate = Float(value.text.tr(",", "."))
      nominal ? rate / nominal.text.to_i : rate
    end
  end
end
