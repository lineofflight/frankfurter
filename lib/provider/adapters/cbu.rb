# frozen_string_literal: true

require "oj"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Central Bank of Uzbekistan. Publishes daily rates for 20+ currencies against UZS. Per-day JSON endpoint at
    # https://cbu.uz/en/arkhiv-kursov-valyut/json/all/{YYYY-MM-DD}/
    class CBU < Adapter
      URL = "https://cbu.uz/en/arkhiv-kursov-valyut/json/all/"

      # The RUB series is not restated across the 1998 redenomination: 1000 "RUB" = 13.46 UZS in the 1997-12-30
      # bulletin, 1 RUB = 13.48 in the next one on 1998-01-06, CBU's first new-ruble bulletin. Earlier rows are old
      # ruble (RUR).
      PREDECESSORS = {
        "RUB" => ["RUR", Date.new(1998, 1, 6)],
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

      def parse(json)
        data = json.is_a?(String) ? Oj.load(json, mode: :strict) : json
        raise "CBU: expected JSON array" unless data.is_a?(Array)

        data.filter_map do |row|
          code = row["Ccy"]
          next unless code&.match?(/\A[A-Z]{3}\z/)

          nominal = Integer(row["Nominal"])
          rate = Float(row["Rate"])
          next if rate.zero? || nominal.zero?

          date = Date.strptime(row["Date"], "%d.%m.%Y")

          { date:, base: historical_code(code, date), quote: "UZS", rate: rate / nominal }
        end
      end

      private

      def fetch_date(date)
        response = http.get("#{URL}#{date.strftime("%Y-%m-%d")}/").to_s
        parse(response)
      end
    end
  end
end
