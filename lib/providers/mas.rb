# frozen_string_literal: true

require "json"
require "net/http"

require "providers/base"

module Providers
  # Monetary Authority of Singapore daily exchange rates.
  # Publishes 21 currencies against SGD via a public JSON API (no auth required).
  # https://eservices.mas.gov.sg/api/action/datastore/search.json
  class MAS < Base
    BASE_URL = "https://eservices.mas.gov.sg/api/action/datastore/search.json"
    RESOURCE_ID = "95932927-c8bc-4e7a-b484-68a66a24edfe"
    EARLIEST_DATE = Date.new(2015, 1, 2)

    # MAS uses flat column names like "usd_sgd", "eur_sgd", etc.
    # Each column value is the amount of SGD per unit of foreign currency
    # (or per 100 units for JPY, KRW, INR, etc.)
    CURRENCY_COLUMNS = {
      "aed_sgd_100" => ["AED", 100],
      "aud_sgd" => ["AUD", 1],
      "cad_sgd" => ["CAD", 1],
      "chf_sgd" => ["CHF", 1],
      "cny_sgd_100" => ["CNY", 100],
      "eur_sgd" => ["EUR", 1],
      "gbp_sgd" => ["GBP", 1],
      "hkd_sgd_100" => ["HKD", 100],
      "idr_sgd_100" => ["IDR", 100],
      "inr_sgd_100" => ["INR", 100],
      "jpy_sgd_100" => ["JPY", 100],
      "krw_sgd_100" => ["KRW", 100],
      "myr_sgd_100" => ["MYR", 100],
      "nzd_sgd" => ["NZD", 1],
      "php_sgd_100" => ["PHP", 100],
      "qar_sgd_100" => ["QAR", 100],
      "sar_sgd_100" => ["SAR", 100],
      "thb_sgd_100" => ["THB", 100],
      "twd_sgd_100" => ["TWD", 100],
      "usd_sgd" => ["USD", 1],
      "vnd_sgd_100" => ["VND", 100],
    }.freeze

    class << self
      def key = "MAS"
      def name = "Monetary Authority of Singapore"
      def earliest_date = EARLIEST_DATE
    end

    def fetch(since: nil, upto: nil)
      @dataset = []
      params = { resource_id: RESOURCE_ID, limit: 10000, sort: "end_of_day asc" }
      params["between[end_of_day]"] = "#{since},#{upto || Date.today}" if since

      url = URI(BASE_URL)
      url.query = URI.encode_www_form(params)

      response = Net::HTTP.get(url)
      data = JSON.parse(response)

      @dataset = parse(data)
      self
    rescue Net::OpenTimeout, Net::ReadTimeout, JSON::ParserError
      self
    end

    def parse(data)
      data = JSON.parse(data) if data.is_a?(String)
      records = data.dig("result", "records") || []

      records.flat_map do |record|
        date = Date.parse(record["end_of_day"])

        CURRENCY_COLUMNS.filter_map do |column, (currency, unit)|
          value = record[column]
          next if value.nil? || value.to_s.strip.empty?

          rate = Float(value)
          next if rate.zero?

          { provider: key, date:, base: currency, quote: "SGD", rate: rate / unit }
        rescue ArgumentError, TypeError
          nil
        end
      end
    end
  end
end
