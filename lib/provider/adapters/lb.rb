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
      # The Turkmen manat switched on the official date: 10000 "TMT" = 1.7354 LTL on 2008-12-31, 10 TMT = 8.677 LTL on
      # 2009-01-01. So did the Belarusian ruble, whose BYR series holds the 1994 ruble until 1999-12-31 (0.000004444
      # LTL) and the 2000 ruble from 2000-01-03 (0.0044444). The rest trail by a day or more:
      #
      #   1000 "PLN" = 0.1641 LTL on 1995-01-02, 1 PLN = 1.646 on 1995-01-03       10000:1 on 1995-01-01
      #   1000 "RUB" = 0.6694 LTL on 1998-01-02, 1 RUB = 0.6672 on 1998-01-05      1000:1 on 1998-01-01
      #   1000 "BGN" = 2.1467 LTL on 1999-07-06, 1 BGN = 2.0952 on 1999-07-07      1000:1 on 1999-07-05
      #   100000 "RON" = 9.5538 LTL on 2005-07-01, 10 RON = 9.5814 on 2005-07-04   10000:1 on 2005-07-01
      #   10000 "MZN" = 1.0536 LTL on 2006-07-07, 10 MZN = 1.0531 on 2006-07-10    1000:1 on 2006-07-01
      PREDECESSORS = {
        "AZN" => ["AZM", Date.new(2006, 1, 9)],
        "BGN" => ["BGL", Date.new(1999, 7, 7)],
        "BYR" => ["BYB", Date.new(2000, 1, 1)],
        "MZN" => ["MZM", Date.new(2006, 7, 10)],
        "PLN" => ["PLZ", Date.new(1995, 1, 3)],
        "RON" => ["ROL", Date.new(2005, 7, 4)],
        "RUB" => ["RUR", Date.new(1998, 1, 5)],
        "TMT" => ["TMM", Date.new(2009, 1, 1)],
      }.freeze

      # YUN is ISO's code for the convertible dinar of 1990 to 1992. LB uses it from 1996-05-07 to 1998-07-06 for the
      # dinar of 1994, at 5 to 11 to the dollar in line with BIS's YUM rates, and quotes YUM at the same level from
      # 1998-07-07: 1 YUN = 0.3734 LTL on 1998-07-06, 1 YUM = 0.3712 on 1998-07-07.
      ALIASES = { "YUN" => "YUM" }.freeze

      # From 1995-01-19 LB carried rows for Georgia, Tajikistan and Turkmenistan that repeat its Russian ruble quote to
      # the digit (on 1995-01-27, the previous day's), until it began quoting each country's own currency. GER, a code
      # no ISO list carries, does so for its whole run to 1995-09-28, and LB quotes the lari from the next day. TJR does
      # until 100 TJR = 7.4074 LTL on 1995-07-11, about 84 rubles and in line with BIS's Tajik rate, and TMT until 100
      # TMT = 0.2759 LTL on 1995-11-30, about 3 rubles. The copies price the ruble, which the RUB row already does, so
      # they're skipped. Each entry is the date the copying stops.
      RUBLE_COPIES = {
        "GER" => Date.new(1995, 9, 29),
        "TJR" => Date.new(1995, 7, 11),
        "TMT" => Date.new(1995, 11, 30),
      }.freeze

      # Belarus denominated its unit of account tenfold on 1994-08-20. Until then prices and non-cash rubles were kept
      # in nominal rubles, a tenth of a banknote ruble; from then the banknote ruble (BYB) was the unit. LB's first BYR
      # rows quote the nominal ruble with the same amount field: 100 "BYR" = 0.0140 LTL on 1994-08-19, 0.1421 on
      # 1994-08-22, while RUB and USD held still. Read as BYB, the old quotes are per 10, which puts them at BIS's
      # month-end 2,800 BYB to the dollar for July 1994.
      BYB_DENOMINATION = Date.new(1994, 8, 20)

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
            next if ruble_copy?(second_ccy, date)

            base_quantity /= 10 if second_ccy == "BYR" && date < BYB_DENOMINATION

            base = historical_code(ALIASES.fetch(second_ccy, second_ccy), date)
            { date:, base:, quote: "LTL", rate: quote_amt / base_quantity }
          else
            rate = Float(second_amt)
            next if rate.zero?

            { date:, base: "EUR", quote: second_ccy, rate: }
          end
        end
      end

      private

      def ruble_copy?(code, date)
        cutover = RUBLE_COPIES[code]
        cutover && date < cutover
      end

      def fetch_date(date)
        tp = date < EUR_ADOPTION ? "LT" : "EU"
        response = http.get(BASE_URL, params: { tp:, dt: date.strftime("%Y-%m-%d") }).to_s
        parse(response)
      end
    end
  end
end
