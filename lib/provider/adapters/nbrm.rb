# frozen_string_literal: true

require "json"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # National Bank of the Republic of North Macedonia. Publishes daily mid-market rates for ~31 currencies against MKD.
    class NBRM < Adapter
      BASE_URL = "https://www.nbrm.mk/KLServiceNOV/GetExchangeRate"
      CHUNK_DAYS = 90

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        dataset = []

        each_chunk(after, end_date) do |chunk_start, chunk_end|
          dataset.concat(fetch_rates(chunk_start, chunk_end))
        end

        dataset
      end

      def parse(json)
        data = json.is_a?(String) ? JSON.parse(json) : json
        raise "NBRM: expected JSON array from GetExchangeRate, got #{data.class}" unless data.is_a?(Array)

        data.filter_map do |row|
          iso = row["oznaka"]&.strip
          next unless iso&.match?(/\A[A-Z]{3}\z/)
          next if iso == "MKD"

          rate = Float(row["sreden"]) / Integer(row["nomin"])
          next if rate.zero?

          date = Date.strptime(row["datum"].split("T").first, "%Y-%m-%d")

          { date:, base: iso, quote: "MKD", rate: }
        end
      end

      private

      def fetch_rates(start_date, end_date)
        response = http.get(BASE_URL, params: {
          StartDate: start_date.strftime("%d.%m.%Y"),
          EndDate: end_date.strftime("%d.%m.%Y"),
          format: "json",
        }).to_s
        parse(response)
      end

      def each_chunk(start_date, end_date)
        current = start_date
        first = true
        while current <= end_date
          sleep(0.5) unless first
          first = false
          chunk_end = [current + CHUNK_DAYS - 1, end_date].min
          yield current, chunk_end
          current = chunk_end + 1
        end
      end
    end
  end
end
