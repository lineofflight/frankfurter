# frozen_string_literal: true

require "json"
require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Hong Kong Monetary Authority. Publishes daily HKD exchange rates for 17
    # currencies via a public JSON API. Rates are expressed as HKD per 1 unit
    # of foreign currency (base = foreign currency, quote = HKD). Data is
    # published monthly with approximately a 1-month lag.
    # Historical data available from 1981-01-02.
    class HKMA < Adapter
      BASE_URL = "https://api.hkma.gov.hk/public/market-data-and-statistics/" \
        "monthly-statistical-bulletin/er-ir/er-eeri-daily"
      PAGE_SIZE = 100

      CURRENCY_FIELDS = [
        "usd",
        "eur",
        "gbp",
        "jpy",
        "cad",
        "aud",
        "sgd",
        "twd",
        "chf",
        "cny",
        "krw",
        "thb",
        "myr",
        "php",
        "inr",
        "idr",
        "zar",
      ].freeze

      class << self
        def backfill_range = 365
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today

        fetch_pages(after, end_date)
      end

      def parse(records)
        records.each_with_object([]) do |record, result|
          date_str = record["end_of_day"]
          next unless date_str

          date = Date.parse(date_str)
          CURRENCY_FIELDS.each do |field|
            rate_value = record[field]
            next unless rate_value.is_a?(Numeric) && rate_value.positive?

            result << { date:, base: field.upcase, quote: "HKD", rate: rate_value.to_f }
          end
        end
      end

      private

      def fetch_pages(start_date, end_date)
        raw_records = []
        offset = 0

        loop do
          page = fetch_page(start_date, end_date, offset)
          break if page.empty?

          raw_records.concat(page)
          break if page.size < PAGE_SIZE

          offset += PAGE_SIZE
        end

        parse(raw_records)
      end

      def fetch_page(start_date, end_date, offset)
        uri = URI(BASE_URL)
        uri.query = URI.encode_www_form(
          "choose" => "end_of_day",
          "from" => start_date.to_s,
          "to" => end_date.to_s,
          "pagesize" => PAGE_SIZE,
          "offset" => offset,
          "sortby" => "end_of_day",
          "sortorder" => "desc",
        )
        response = Net::HTTP.get(uri)
        data = JSON.parse(response)
        data.dig("result", "records") || []
      end
    end
  end
end
