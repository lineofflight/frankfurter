# frozen_string_literal: true

require "net/http"
require "rexml/document"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Bank of Lithuania (Lietuvos Bankas). Publishes daily exchange rates for
    # ~88 currencies. Pre-2015 rates are quoted against LTL (Lithuanian litas);
    # post-2015 rates are EUR-based (ECB rates republished after euro adoption).
    class LB < Adapter
      BASE_URL = "https://www.lb.lt/webservices/FxRates/FxRates.asmx/getFxRates"
      EUR_ADOPTION = Date.new(2015, 1, 1)
      NAMESPACE = "http://www.lb.lt/WebServices/FxRates"

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
        doc = REXML::Document.new(xml)
        records = []

        doc.each_element("//FxRate") do |fx_rate|
          amounts = fx_rate.get_elements("CcyAmt")
          next unless amounts.size == 2

          first_ccy = amounts[0].get_text("Ccy")&.to_s
          first_amt = amounts[0].get_text("Amt")&.to_s
          second_ccy = amounts[1].get_text("Ccy")&.to_s
          second_amt = amounts[1].get_text("Amt")&.to_s
          date_str = fx_rate.get_text("Dt")&.to_s

          next unless first_ccy && first_amt && second_ccy && second_amt && date_str

          date = Date.parse(date_str)
          tp = fx_rate.get_text("Tp")&.to_s

          if tp == "LT"
            # Pre-EUR: first is LTL amount, second is foreign currency quantity
            quote_amt = Float(first_amt)
            base_quantity = Float(second_amt)
            next if quote_amt.zero? || base_quantity.zero?

            records << { date:, base: second_ccy, quote: "LTL", rate: quote_amt / base_quantity }
          else
            # Post-EUR (EU): first is EUR=1, second is foreign currency rate
            rate = Float(second_amt)
            next if rate.zero?

            records << { date:, base: "EUR", quote: second_ccy, rate: }
          end
        end

        records
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
