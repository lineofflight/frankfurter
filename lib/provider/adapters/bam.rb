# frozen_string_literal: true

require "oj"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Bank Al-Maghrib. Publishes daily mid-market rates for ~30 currencies against MAD. Older data (pre-2016) lacks a
    # mid-rate field; the adapter averages buy/sell.
    class BAM < Adapter
      URL = "https://api.centralbankofmorocco.ma/cours/Version1/api/CoursVirement"

      # The ouguiya keeps its MRO label after the 2018 redenomination: 100 MRO = 2.624 MAD on 2018-01-02 and 26.309 on
      # 2018-01-03, with the new ouguiya at 0.263 MAD. It is quoted per 100 throughout, but on 53 days in 2018 the unit
      # field reads 1 (25.857 MAD on 2018-04-17, against 25.864 per 100 the day before).
      SUCCESSORS = { "MRO" => ["MRU", Date.new(2018, 1, 3)] }.freeze

      class << self
        def api_key = ENV["BAM_API_KEY"] || raise("no API key")

        def backfill_range = 7
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today

        (after..end_date).each_with_object([]) do |date, dataset|
          next if date.saturday? || date.sunday?

          dataset.concat(fetch_date(date))
        end
      end

      def parse(json)
        data = json.is_a?(String) ? Oj.load(json, mode: :strict) : json
        raise "BAM: expected JSON array from #{URL}, got #{data.class}" unless data.is_a?(Array)

        data.filter_map do |record|
          code = record["libDevise"]
          next unless code&.match?(/\A[A-Z]{3}\z/)

          date = Date.parse(record["date"])
          mid = record["moyen"]&.to_f || midpoint(record["achat"].to_f, record["vente"].to_f)
          unite = record["uniteDevise"].to_f
          unite = 100.0 if code == "MRO" && unite == 1
          next if mid.zero? || unite.zero?

          { date:, base: historical_code(code, date), quote: "MAD", rate: mid / unite,
            **prices(bid: record["achat"], ask: record["vente"], mid: record["moyen"], unit: unite), }
        end
      end

      private

      def fetch_date(date)
        response = http
          .headers("Ocp-Apim-Subscription-Key" => self.class.api_key)
          .get(URL, params: { date: "#{date.strftime("%Y-%m-%d")}T12:30:00" })

        sleep(1)
        parse(response.to_s)
      end
    end
  end
end
