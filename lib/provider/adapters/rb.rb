# frozen_string_literal: true

require "json"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Sveriges Riksbank. Fetches daily exchange rates for ~29 currencies
    # against the Swedish krona (SEK) via the SWEA API. Uses the ByGroup
    # endpoint (group 130) to fetch all currency series in a single request.
    # The ByGroup endpoint has a max 1-year date range.
    class RB < Adapter
      BASE_URL = "https://api.riksbank.se/swea/v1/Observations/ByGroup/130"

      # The euro succeeded the ECU 1:1 on its first quoting day, 1999-01-04. The Riksbank backfills its EUR series with
      # ECU values before then; relabel those to the ECU's ISO code (XEU) so they stay out of the euro series, matching
      # how the BdP and AMCM adapters label the same data.
      EURO_INCEPTION = Date.new(1999, 1, 4)

      class << self
        def backfill_range = 365
      end

      def fetch(after: nil, upto: nil)
        response = http.get("#{BASE_URL}/#{after}/#{upto || Date.today}").to_s
        parse(response)
      end

      def parse(json)
        data = json.is_a?(String) ? JSON.parse(json) : json
        return [] unless data.is_a?(Array)

        data.filter_map do |obs|
          series_id = obs["seriesId"]
          date_str = obs["date"]
          value = obs["value"]
          next unless series_id && date_str && value

          currency = series_id.delete_prefix("SEK").delete_suffix("PMI")
          next if currency == "ETT"

          rate = Float(value)
          next if rate.zero?

          date = Date.parse(date_str)
          currency = "XEU" if currency == "EUR" && date < EURO_INCEPTION

          { date:, base: currency, quote: "SEK", rate: }
        end
      end
    end
  end
end
