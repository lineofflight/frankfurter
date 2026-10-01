# frozen_string_literal: true

require "json"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # National Bank of Poland. Publishes daily mid-market rates (Table A) for ~32 currencies and weekly mid-market rates
    # (Table B) for ~150 additional currencies against PLN, plus a daily gold reference price. Gold values come in PLN
    # per gram and are normalized here to per troy ounce.
    class NBP < Adapter
      TABLE_A_URL = "https://api.nbp.pl/api/exchangerates/tables/A"
      TABLE_B_URL = "https://api.nbp.pl/api/exchangerates/tables/B"
      GOLD_URL = "https://api.nbp.pl/api/cenyzlota"

      # Table B carried retired codes until table 23/B/NBP/2003 (2003-11-12): AON for the kwanza, at AOA values (0.0503
      # PLN on 2003-10-28, 0.0511 as AOA next), and BYB for the Belarusian ruble in tables 5 to 18 of 2002, with BYR
      # before and after.
      ALIASES = { "AON" => "AOA", "BYB" => "BYR" }.freeze

      # AFA rows switch to the new afghani on 2003-01-07 (0.000816 PLN on 2002-12-24, 0.089056 next) and keep the old
      # label until AFN replaces it in the same 2003-11-12 table. ZWR rows switch to the 2009 Zimbabwe dollar on
      # 2009-02-25 (0.00000001 PLN on 2009-02-04, 0.043749 next) and keep the old label until ZWL on 2010-06-02.
      SUCCESSORS = {
        "AFA" => ["AFN", Date.new(2003, 1, 7)],
        "ZWR" => ["ZWL", Date.new(2009, 2, 25)],
      }.freeze

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        dataset = []

        each_chunk(after, end_date) do |chunk_start, chunk_end|
          dataset.concat(fetch_rates(TABLE_A_URL, chunk_start, chunk_end))
          dataset.concat(fetch_rates(TABLE_B_URL, chunk_start, chunk_end))
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

            { date:, base: historical_code(ALIASES.fetch(iso, iso), date), quote: "PLN", rate: mid }
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
        parse(http.get("#{table_url}/#{start_date}/#{end_date}/?format=json").to_s)
      rescue HTTP::StatusError => e
        # This happens if the date range includes no working days
        raise unless e.response.code == 404

        []
      end

      def fetch_gold(start_date, end_date)
        parse_gold(http.get("#{GOLD_URL}/#{start_date}/#{end_date}/?format=json").to_s)
      rescue HTTP::StatusError => e
        # This happens if the date range includes no working days
        raise unless e.response.code == 404

        []
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
