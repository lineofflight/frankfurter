# frozen_string_literal: true

require "net/http"
require "ox"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Bank of Lithuania (Lietuvos Bankas). Publishes daily exchange rates for
    # ~88 currencies. Pre-2015 rates are quoted against LTL (Lithuanian litas);
    # post-2015 rates are EUR-based (ECB rates republished after euro adoption).
    class LB < Adapter
      BASE_URL = "https://www.lb.lt/webservices/FxRates/FxRates.asmx/getFxRates"
      EUR_ADOPTION = Date.new(2015, 1, 1)

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

        doc.locate("*/FxRate").filter_map do |fx_rate|
          amounts = fx_rate.locate("CcyAmt")
          next unless amounts.size == 2

          first_ccy = amounts[0].locate("Ccy/^String").first
          first_amt = amounts[0].locate("Amt/^String").first
          second_ccy = amounts[1].locate("Ccy/^String").first
          second_amt = amounts[1].locate("Amt/^String").first
          date_str = fx_rate.locate("Dt/^String").first

          next unless first_ccy && first_amt && second_ccy && second_amt && date_str

          date = Date.parse(date_str)
          tp = fx_rate.locate("Tp/^String").first

          if tp == "LT"
            quote_amt = Float(first_amt)
            base_quantity = Float(second_amt)
            next if quote_amt.zero? || base_quantity.zero?

            { date:, base: second_ccy, quote: "LTL", rate: quote_amt / base_quantity }
          else
            rate = Float(second_amt)
            next if rate.zero?

            { date:, base: "EUR", quote: second_ccy, rate: }
          end
        end
      end

      private

      def fetch_date(date)
        tp = date < EUR_ADOPTION ? "LT" : "EU"
        url = URI(BASE_URL)
        url.query = URI.encode_www_form(tp:, dt: date.strftime("%Y-%m-%d"))
        response = Net::HTTP.get(url)
        parse(response)
      end
    end
  end
end
