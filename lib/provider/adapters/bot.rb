# frozen_string_literal: true

require "json"
require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Bank of Thailand. Fetches daily average commercial bank exchange rates
    # for 19 currencies against the Thai baht (THB). Uses mid_rate (midpoint
    # of buying transfer and selling). Some currencies are quoted per 100 or
    # 1,000 units — the adapter normalises to per-unit rates.
    # Requires BOT_API_KEY environment variable.
    class BOT < Adapter
      BASE_URL = "https://gateway.api.bot.or.th/Stat-ExchangeRate/v2/DAILY_AVG_EXG_RATE/"

      class << self
        def api_key = ENV["BOT_API_KEY"] || raise(Adapter::ApiKeyMissing)
        def backfill_range = 30 # API enforces max 31-day period per request
      end

      def fetch(after: nil, upto: nil)
        uri = URI(BASE_URL)
        uri.query = URI.encode_www_form(
          start_period: after.strftime("%Y-%m-%d"),
          end_period: (upto || Date.today).strftime("%Y-%m-%d"),
        )

        response = Net::HTTP.get_response(uri, {
          "Authorization" => self.class.api_key,
          "Accept" => "application/json",
        })

        parse(response.body)
      end

      def parse(body)
        data = JSON.parse(body).dig("result", "data", "data_detail")
        return [] unless data

        data.filter_map do |record|
          mid = record["mid_rate"]
          next if mid.nil? || mid.to_s.empty?

          rate = Float(mid)
          next if rate.zero?

          currency = record["currency_id"]
          unit = extract_unit(record["currency_name_eng"])
          rate /= unit if unit > 1

          { date: Date.parse(record["period"]), base: currency, quote: "THB", rate: }
        end
      end

      private

      def extract_unit(name)
        case name
        when /\(100 /i then 100
        when /\(1,000 /i then 1_000
        else 1
        end
      end
    end
  end
end
