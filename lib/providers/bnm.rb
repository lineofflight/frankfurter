# frozen_string_literal: true

require "json"
require "net/http"

require "providers/base"

module Providers
  # Bank Negara Malaysia daily exchange rates. Data available from 2021-01-04.
  # Historical rates are fetched per-currency per-month.
  class BNM < Base
    BASE_URL = "https://api.bnm.gov.my/public/exchange-rate"
    EARLIEST_DATE = "2021-01-04"
    SESSION = "0900"
    HEADERS = { "Accept" => "application/vnd.BNM.API.v1+json" }.freeze

    class << self
      def key = "BNM"
      def name = "Bank Negara Malaysia"
    end

    def fetch(since: nil, upto: nil)
      start_date = Date.parse((since || EARLIEST_DATE).to_s)
      end_date = upto || Date.today
      currencies = fetch_currencies
      @dataset = currencies.flat_map { |code| fetch_currency(code, start_date, end_date) }
      self
    end

    private

    def fetch_currencies
      uri = URI("#{BASE_URL}?session=#{SESSION}")
      response = Net::HTTP.start(uri.host, uri.port, use_ssl: true) do |http|
        http.get("#{uri.path}?#{uri.query}", HEADERS)
      end
      data = JSON.parse(response.body)
      data["data"].map { |item| item["currency_code"] }
    end

    def fetch_currency(code, start_date, end_date)
      records = []
      each_month(start_date, end_date) do |year, month|
        uri = URI("#{BASE_URL}/#{code}/year/#{year}/month/#{month}?session=#{SESSION}")
        response = Net::HTTP.start(uri.host, uri.port, use_ssl: true) do |http|
          http.get("#{uri.path}?#{uri.query}", HEADERS)
        end
        data = JSON.parse(response.body)
        currency_data = data["data"]
        next if currency_data.nil? || currency_data.is_a?(Array)

        unit = currency_data["unit"] || 1
        rates = currency_data["rate"]
        rates = [rates] unless rates.is_a?(Array)

        rates.each do |rate|
          mid = rate["middle_rate"]
          next unless mid

          date = Date.parse(rate["date"])
          next if date > end_date

          records << { provider: key, date:, base: code, quote: "MYR", rate: mid / unit }
        end
      end
      records
    end

    def each_month(start_date, end_date)
      date = Date.new(start_date.year, start_date.month, 1)
      while date <= end_date
        yield date.year, date.month
        date = date.next_month
      end
    end
  end
end
