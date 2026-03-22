# frozen_string_literal: true

require "json"
require "net/http"

require "providers/base"

module Providers
  # Central Bank of the Republic of Turkey.
  # Uses EVDS3 bulk API with buying (A) and selling (S) rates in TRY.
  # Each currency's mid-market rate is derived from the average of buy/sell.
  # Requires TCMB_API_KEY environment variable.
  class TCMB < Base
    EVDS_URL = "https://evds3.tcmb.gov.tr/igmevdsms-dis"
    EARLIEST_DATE = Date.new(1996, 4, 16)

    # Buy/sell rates in TRY for each currency. Hardcoded because the EVDS3 catalog API doesn't expose
    # a clean list and the series rarely change.
    # Browse: https://evds3.tcmb.gov.tr > Exchange Rates > Indicative Exchange Rates
    CURRENCIES = [
      "AED",
      "AUD",
      "AZN",
      "CAD",
      "CHF",
      "CNY",
      "DKK",
      "EUR",
      "GBP",
      "JPY",
      "KRW",
      "KWD",
      "KZT",
      "NOK",
      "PKR",
      "QAR",
      "RON",
      "RUB",
      "SAR",
      "SEK",
      "USD",
    ].freeze

    SERIES = CURRENCIES.flat_map do |c|
      [["#{c}_BUY", "TP.DK.#{c}.A.YTL"], ["#{c}_SELL", "TP.DK.#{c}.S.YTL"]]
    end.to_h.freeze

    COLUMNS = SERIES.to_h { |code, series| [series.tr(".", "_"), code] }.freeze

    class << self
      def key = "TCMB"
      def name = "Central Bank of Turkey"
    end

    def fetch(since: nil, upto: nil)
      return no_key unless api_key

      start_date = since || EARLIEST_DATE
      start_date = Date.parse(start_date.to_s)
      end_date = Date.today
      @dataset = []

      each_chunk(start_date, end_date) do |chunk_start, chunk_end|
        @dataset.concat(fetch_rates(chunk_start, chunk_end))
      end

      self
    end

    private

    def api_key
      ENV["TCMB_API_KEY"]
    end

    def no_key
      @dataset = []
      self
    end

    def fetch_rates(start_date, end_date)
      url = URI("#{EVDS_URL}/series=#{SERIES.values.join("-")}" \
        "&startDate=#{start_date.strftime("%d-%m-%Y")}" \
        "&endDate=#{end_date.strftime("%d-%m-%Y")}" \
        "&type=json&frequency=1")

      response = Net::HTTP.get(url, "key" => api_key)
      data = JSON.parse(response)
      items = data["items"] || []

      items.flat_map do |item|
        date = Date.strptime(item["Tarih"], "%d-%m-%Y")
        raw = {}

        COLUMNS.each do |column, code|
          value = item[column]
          next if value.nil?

          raw[code] = Float(value)
        end

        # Mid of buying and selling for each currency → X→TRY rate
        # JPY is quoted per 100 units in TCMB data (confirmed via series metadata)
        CURRENCIES.filter_map do |currency|
          buy = raw["#{currency}_BUY"]
          sell = raw["#{currency}_SELL"]
          next unless buy && sell

          rate = (buy + sell) / 2
          rate /= 100.0 if currency == "JPY"

          { provider: key, date:, base: currency, quote: "TRY", rate: rate.round(4) }
        end
      end
    end

    def each_chunk(start_date, end_date)
      current = start_date
      first = true
      while current <= end_date
        sleep(1) unless first
        first = false
        chunk_end = [current >> 24, end_date].min
        yield current, chunk_end
        current = chunk_end + 1
      end
    end
  end
end
