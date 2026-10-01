# frozen_string_literal: true

require "oj"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # National Bank of Ukraine. Publishes daily rates for ~45 currencies against UAH.
    class NBU < Adapter
      URL = "https://bank.gov.ua/NBU_Exchange/exchange_site"

      # The TJS series starts in 1999 under the Tajikistani ruble, which the somoni replaced at 1000:1 on 2000-10-30,
      # and is not restated: 1000 "TJS" = 2.78 UAH on 2000-09-01 against 1 TJS = 1.81 in 2002. Earlier rows are TJR.
      PREDECESSORS = {
        "TJS" => ["TJR", Date.new(2000, 10, 30)],
      }.freeze

      # The archive keeps six retired codes after their redenominations and quotes the successor under them, until the
      # current codes replace them in April 2014 (RUB in 2004). Each entry is the first date in the new unit, which can
      # trail the official one by a few days.
      SUCCESSORS = {
        "RUR" => ["RUB", Date.new(1998, 1, 1)],
        "BGL" => ["BGN", Date.new(1999, 8, 1)],
        "TRL" => ["TRY", Date.new(2005, 1, 6)],
        "ROL" => ["RON", Date.new(2005, 7, 1)],
        "AZM" => ["AZN", Date.new(2006, 1, 6)],
        "TMM" => ["TMT", Date.new(2009, 1, 6)],
      }.freeze

      # From these dates the rate is per 100 units of the successor while the units field still reads 1000 or 10000:
      # 10000 "TRL" = 372.9741 UAH on 2005-06-27, with the new lira at 3.73 UAH, and 100 TRY = 541.6837 when the label
      # changes on 2014-04-04.
      PER_HUNDRED = {
        "BGL" => Date.new(2000, 1, 1),
        "TRL" => Date.new(2005, 1, 6),
        "ROL" => Date.new(2005, 7, 1),
        "AZM" => Date.new(2006, 1, 6),
        "TMM" => Date.new(2009, 1, 6),
      }.freeze

      class << self
        def backfill_range = 365
      end

      def fetch(after: nil, upto: nil)
        response = http.get(URL, params: {
          start: after.strftime("%Y%m%d"),
          end: (upto || Date.today).strftime("%Y%m%d"),
          sort: "exchangedate",
          order: "asc",
          json: "",
        },).to_s

        parse(response)
      end

      def parse(json)
        data = json.is_a?(String) ? Oj.load(json, mode: :strict) : json

        data.filter_map do |row|
          date = Date.strptime(row.fetch("exchangedate"), "%d.%m.%Y")
          next if date.saturday? || date.sunday?

          iso = row.fetch("cc")
          next unless iso.match?(/\A[A-Z]{3}\z/)

          units = row.fetch("units", 1).to_f
          per_hundred = PER_HUNDRED[iso]
          units = 100.0 if per_hundred && date >= per_hundred
          rate = row.fetch("rate").to_f
          next if rate.zero? || units.zero?

          { date:, base: historical_code(iso, date), quote: "UAH", rate: rate / units }
        end
      end
    end
  end
end
