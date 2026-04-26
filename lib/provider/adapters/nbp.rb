# frozen_string_literal: true

require "json"
require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # National Bank of Poland. Publishes daily mid-market rates (Table A) for ~32 currencies
    # and weekly mid-market rates (Table B) for ~150 additional currencies against PLN,
    # plus a daily gold reference price. Gold values come in PLN per gram and are
    # normalized here to per troy ounce.
    class NBP < Adapter
      TABLE_A_URL = "https://api.nbp.pl/api/exchangerates/tables/A"
      TABLE_B_URL = "https://api.nbp.pl/api/exchangerates/tables/B"
      GOLD_URL = "https://api.nbp.pl/api/cenyzlota"
      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        dataset = []

        [TABLE_A_URL, TABLE_B_URL].each do |table_url|
          each_chunk(after, end_date) do |chunk_start, chunk_end|
            dataset.concat(fetch_rates(table_url, chunk_start, chunk_end))
          end
        end

        each_chunk(after, end_date) do |chunk_start, chunk_end|
          dataset.concat(fetch_gold(chunk_start, chunk_end))
        end

        dataset
      end

      def parse(json)
        data = json.is_a?(String) ? JSON.parse(json) : json

        data.flat_map do |table|
          date = Date.parse(table["effectiveDate"])
          table["rates"].filter_map do |rate|
            iso = rate["code"]
            mid = rate["mid"]
            next unless iso.match?(/\A[A-Z]{3}\z/)
            next if mid.nil? || mid.zero?

            { date:, base: iso, quote: "PLN", rate: mid }
          end
        end
      end

      def parse_gold(json)
        data = json.is_a?(String) ? JSON.parse(json) : json

        data.filter_map do |row|
          price = row["cena"]
          next if price.nil? || price.zero?

          { date: Date.parse(row["data"]), base: "XAU", quote: "PLN", rate: price * GRAMS_PER_TROY_OUNCE }
        end
      end

      private

      def fetch_rates(table_url, start_date, end_date)
        url = URI("#{table_url}/#{start_date}/#{end_date}/?format=json")
        response = Net::HTTP.get_response(url)

        # This happens if the date range includes no working days
        return [] if response.is_a?(Net::HTTPNotFound)

        parse(response.body)
      end

      def fetch_gold(start_date, end_date)
        url = URI("#{GOLD_URL}/#{start_date}/#{end_date}/?format=json")
        response = Net::HTTP.get_response(url)

        # This happens if the date range includes no working days
        return [] if response.is_a?(Net::HTTPNotFound)

        parse_gold(response.body)
      end

      # NBP API limits queries to 93 days per request
      def each_chunk(start_date, end_date)
        current = start_date
        first = true
        while current <= end_date
          sleep(0.5) unless first
          first = false
          chunk_end = [current + 92, end_date].min
          yield current, chunk_end
          current = chunk_end + 1
        end
      end
    end
  end
end
