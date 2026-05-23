# frozen_string_literal: true

require "net/http"
require "ox"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # National Bank of Tajikistan. Publishes daily rates for ~36 currencies against TJS,
    # back to 2001-01-01. Snapshot endpoint returns one date per request and carries
    # forward the most recent trading-day rate on weekends and holidays.
    #
    # The Valute ID attribute is unreliable for historical records (e.g. ID 810
    # appears with CharCode RUB after originally tagging the Soviet rouble SUR).
    # Trust CharCode for ISO mapping. Nominal may be 10, 100, or 1000 for
    # low-value currencies; divide Value by Nominal to normalize to per-unit.
    #
    # Out-of-range requests (before 2001 or beyond today) silently return today's
    # snapshot, so we verify the response's Date attribute matches the request
    # before keeping the records.
    #
    # Records are returned in NBT's native direction — foreign currency as base,
    # TJS as quote — matching other pivot-in-quote adapters (e.g. NBG, BBK).
    #
    # Attribution required: reference to www.nbt.tj per the site footer.
    class NBT < Adapter
      URL = "https://www.nbt.tj/en/kurs/export_xml.php"

      class << self
        def backfill_range = 1
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        dataset = []

        (after..end_date).each do |date|
          dataset.concat(fetch_date(date))
        end

        dataset
      end

      def parse(xml, expected_date: nil)
        doc = Ox.load(xml)
        root = doc.locate("ValCurs").first
        return [] unless root

        date_attr = root[:Date]
        return [] unless date_attr

        date = Date.parse(date_attr)
        return [] if expected_date && date != expected_date

        root.locate("Valute").filter_map do |valute|
          code = valute.locate("CharCode/^String").first
          next unless code&.match?(/\A[A-Z]{3}\z/)

          nominal_str = valute.locate("Nominal/^String").first
          value_str = valute.locate("Value/^String").first
          next unless nominal_str && value_str

          nominal = Integer(nominal_str, exception: false)
          value = Float(value_str, exception: false)
          next unless nominal && value
          next if nominal.zero? || value.zero?

          { date:, base: code, quote: "TJS", rate: value / nominal }
        end
      end

      private

      def fetch_date(date)
        sleep(0.2)
        url = URI(URL)
        url.query = URI.encode_www_form(date: date.strftime("%Y-%m-%d"), export: "xmlout")
        response = Net::HTTP.get(url)
        parse(response, expected_date: date)
      end
    end
  end
end
