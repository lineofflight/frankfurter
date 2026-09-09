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
          rate = row.fetch("rate").to_f
          next if rate.zero? || units.zero?

          { date:, base: historical_code(iso, date), quote: "UAH", rate: rate / units }
        end
      end
    end
  end
end
