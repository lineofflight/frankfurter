# frozen_string_literal: true

require "json"
require "net/http"

require "providers/base"

module Providers
  # Hong Kong Monetary Authority. Publishes daily HKD exchange rates for 17
  # currencies via a public JSON API. Rates are expressed as HKD per 1 unit
  # of foreign currency (base = foreign currency, quote = HKD). Data is
  # published monthly with approximately a 1-month lag.
  # Historical data available from 1981-01-02.
  class HKMA < Base
    BASE_URL = "https://api.hkma.gov.hk/public/market-data-and-statistics/" \
      "monthly-statistical-bulletin/er-ir/er-eeri-daily"
    EARLIEST_DATE = Date.new(1981, 1, 2)
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
      def key = "HKMA"
      def name = "Hong Kong Monetary Authority"
      def earliest_date = EARLIEST_DATE

      def backfill(range: 365)
        super
      end
    end

    def fetch(since: nil, upto: nil)
      start_date = since || EARLIEST_DATE
      end_date = upto || Date.today

      @dataset = fetch_pages(start_date, end_date)
      self
    rescue Net::OpenTimeout, Net::ReadTimeout
      @dataset = []
      self
    end

    def parse(records)
      records.each_with_object([]) do |record, result|
        date_str = record["end_of_day"]
        next unless date_str

        date = Date.parse(date_str)
        CURRENCY_FIELDS.each do |field|
          rate_value = record[field]
          next unless rate_value.is_a?(Numeric) && rate_value.positive?

          result << { provider: key, date:, base: field.upcase, quote: "HKD", rate: rate_value.to_f }
        end
      rescue ArgumentError, TypeError
        nil
      end
    end

    private

    def fetch_pages(start_date, end_date)
      raw_records = []
      offset = 0

      loop do
        page = fetch_page(offset)
        break if page.empty?

        past_start = false
        page.each do |record|
          date = Date.parse(record["end_of_day"].to_s)
          if date < start_date
            past_start = true
            break
          end
          raw_records << record if date <= end_date
        rescue ArgumentError
          next
        end

        break if past_start || page.size < PAGE_SIZE

        offset += PAGE_SIZE
      end

      parse(raw_records)
    end

    def fetch_page(offset)
      uri = URI(BASE_URL)
      uri.query = URI.encode_www_form(
        "pagesize" => PAGE_SIZE,
        "offset" => offset,
        "sortby" => "end_of_day",
        "sortorder" => "desc",
      )
      response = Net::HTTP.get(uri)
      data = JSON.parse(response)
      data.dig("result", "records") || []
    rescue JSON::ParserError
      []
    end
  end
end
