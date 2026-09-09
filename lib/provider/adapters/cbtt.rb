# frozen_string_literal: true

require "oj"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Central Bank of Trinidad and Tobago. Publishes daily buying and selling rates for 8 currencies against the
    # Trinidad and Tobago dollar (TTD), weighted averages of the day's authorised-dealer transactions (GYD and JMD are
    # not weighted). The adapter takes the mid. A leg with no trades that day comes back null, and the mid is skipped
    # when either is missing. History runs from 1991 via a public WordPress REST route that takes a year span; the
    # latest-only route uses a different key shape (and carries JPY without a history) and is not used.
    class CBTT < Adapter
      BASE_URL = "https://www.central-bank.org.tt/wp-json/rates/v1/forex-rate"

      # Column prefix in the payload => ISO code.
      CURRENCIES = {
        "USD" => "USD",
        "CAD" => "CAD",
        "GBP" => "GBP",
        "Euro" => "EUR",
        "CHF" => "CHF",
        "BBD" => "BBD",
        "GYD" => "GYD",
        "JMD" => "JMD",
      }.freeze

      def fetch(after: nil, upto: nil)
        upto ||= Date.today
        span = after ? "#{after.year}-#{upto.year}" : "all"
        response = http.get("#{BASE_URL}/#{span}").to_s

        parse(response).select { |r| (after.nil? || r[:date] >= after) && r[:date] <= upto }
      end

      def parse(json)
        data = Oj.load(json, mode: :strict)
        rows = data.fetch("cbttdailyforexrates") { raise "CBTT: no cbttdailyforexrates in payload from #{BASE_URL}" }
        # A span with no rows comes back as a single all-null object rather than an empty array.
        rows = [] unless rows.is_a?(Array)

        rows.flat_map do |row|
          date = Date.strptime(row.fetch("famedate"), "%Y-%m-%d")

          CURRENCIES.filter_map do |prefix, base|
            buy = row["#{prefix}_Buying"]
            sell = row["#{prefix}_Selling"]
            next unless buy && sell

            rate = midpoint(buy, sell)
            next if rate.zero?

            { date:, base:, quote: "TTD", rate: }
          end
        end
      end
    end
  end
end
