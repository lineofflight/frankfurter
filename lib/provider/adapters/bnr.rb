# frozen_string_literal: true

require "net/http"
require "ox"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # National Bank of Romania. Publishes daily reference rates for ~35
    # currencies against the Romanian leu (RON). Current rates via the 10-day
    # XML feed; historical data via yearly XML archives back to 2005.
    class BNR < Adapter
      BASE_URL = "https://www.bnr.ro"
      CURRENT_URL = URI("#{BASE_URL}/nbrfxrates10days.xml")

      class << self
        def backfill_range = 365
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today

        records = if after.nil? || after.year == end_date.year
          fetch_year(after&.year || end_date.year)
        else
          (after.year..end_date.year).flat_map { |year| fetch_year(year) }
        end

        records.select { |r| (after.nil? || r[:date] > after) && r[:date] <= end_date }
      end

      def parse(xml)
        doc = Ox.load(xml)

        doc.locate("DataSet/Body/Cube").flat_map do |cube|
          date = Date.parse(cube[:date])
          cube.locate("Rate").filter_map do |rate_node|
            currency = rate_node[:currency]
            next unless currency&.match?(/\A[A-Z]{3}\z/)

            multiplier = rate_node[:multiplier]&.to_i || 1
            rate_value = Float(rate_node.text)
            rate_value /= multiplier if multiplier > 1
            next if rate_value.zero?

            { date:, base: currency, quote: "RON", rate: rate_value }
          end
        end
      end

      private

      def fetch_year(year)
        url = if year >= Date.today.year
          CURRENT_URL
        else
          URI("#{BASE_URL}/files/xml/years/nbrfxrates#{year}.xml")
        end

        parse(Net::HTTP.get(url))
      end
    end
  end
end
