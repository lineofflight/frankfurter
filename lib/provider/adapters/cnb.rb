# frozen_string_literal: true

require "oj"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Czech National Bank. Publishes daily exchange rates for 30 currencies against the Czech koruna (CZK) via a REST
    # JSON API.
    class CNB < Adapter
      URL = "https://api.cnb.cz/cnbapi/exrates/daily-year"

      # The first weeks of 1991 use two codes ISO had already retired. BEC, the convertible franc of the two-tier market
      # Belgium abolished in 1990, is the Belgian franc: it matches LUF to the digit until 1991-01-24 and BEF takes over
      # the next day, 100 BEC = 88.99 CZK then 100 BEF = 89.31. YUD, the hard dinar replaced at 10,000:1 on 1990-01-01,
      # is the convertible dinar on its three days: 1 "YUD" = 2.04 CZK on 1991-01-03 with DEM at 18.39, or 9 dinars to
      # the mark, where hard dinars would run to tens of thousands.
      #
      # XCU is not aliased. It is the clearing ECU ("cl. ECU") that settled Czech-Slovak payments from 1993-02-08 to
      # 1995-09-29, a unit of its own registered in db/seeds/currency_patches.json. CNB mostly valued it at the ECU, but
      # 2% below from 1993-03-08 to 1993-07-12 (33.436 CZK against the ECU's 34.118 on 1993-03-10) and 3% below from
      # 1993-12-03 to 1994-03-03.
      ALIASES = { "BEC" => "BEF", "YUD" => "YUN" }.freeze

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        dataset = []

        (after.year..end_date.year).each do |year|
          dataset.concat(fetch_year(year))
        end

        dataset.select { |r| r[:date].between?(after, end_date) }
      end

      def parse(json)
        data = json.is_a?(String) ? Oj.load(json, mode: :strict) : json
        rates = data.is_a?(Hash) ? data["rates"] : nil
        raise "CNB: no rates array in daily-year response" unless rates.is_a?(Array)

        # The API 200s with {"rates":[]} for a year before its first fixing (e.g. New Year's Day).
        return [] if rates.empty?

        rates.filter_map do |r|
          code = r["currencyCode"]
          next unless code&.match?(/\A[A-Z]{3}\z/)

          amount = r["amount"].to_f
          rate = r["rate"].to_f
          next if rate.zero? || amount.zero?

          date = Date.parse(r["validFor"])
          { date:, base: ALIASES.fetch(code, code), quote: "CZK", rate: rate / amount }
        end
      end

      private

      def fetch_year(year)
        parse(http.get(URL, params: { year: year, lang: "EN" }).to_s)
      end
    end
  end
end
