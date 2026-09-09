# frozen_string_literal: true

require "ox"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Bank of Lithuania (Lietuvos Bankas). Publishes daily exchange rates for ~88 currencies. Pre-2015 rates are quoted
    # against LTL (Lithuanian litas); post-2015 rates are EUR-based (ECB rates republished after euro adoption).
    class LB < Adapter
      BASE_URL = "https://www.lb.lt/webservices/FxRates/FxRates.asmx/getFxRates"
      EUR_ADOPTION = Date.new(2015, 1, 1)

      # LB labels a redenominated currency's whole history with its current code without restating the values: the
      # 2005-12-30 bulletin quotes 1 "AZN" = 0.00063 LTL, old manat. Each entry maps the current code to its predecessor
      # and the first date LB's values switch to the successor, which can trail the official date: the manat was
      # redenominated on 2006-01-01, but LB kept quoting old manat through 2006-01-06 and jumped 5000x on 2006-01-09.
      PREDECESSORS = {
        "AZN" => ["AZM", Date.new(2006, 1, 9)],
      }.freeze

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

            { date:, base: historical_code(second_ccy, date), quote: "LTL", rate: quote_amt / base_quantity }
          else
            rate = Float(second_amt)
            next if rate.zero?

            { date:, base: "EUR", quote: second_ccy, rate: }
          end
        end
      end

      private

      def historical_code(code, date)
        predecessor, cutover = PREDECESSORS[code]
        predecessor && date < cutover ? predecessor : code
      end

      def fetch_date(date)
        tp = date < EUR_ADOPTION ? "LT" : "EU"
        response = http.get(BASE_URL, params: { tp:, dt: date.strftime("%Y-%m-%d") }).to_s
        parse(response)
      end
    end
  end
end
