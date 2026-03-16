# frozen_string_literal: true

require "json"
require "net/http"

require "providers/base"

module Providers
  # Central Bank of the Republic of Turkey.
  # Uses EVDS3 bulk API with cross rates (mid-market, USD base).
  # Requires TCMB_API_KEY environment variable.
  class TCMB < Base
    EVDS_URL = "https://evds3.tcmb.gov.tr/igmevdsms-dis"
    EARLIEST_DATE = Date.new(1996, 4, 16)

    # Cross rates (C) are mid-market rates expressed in USD. TRY is derived from the mid of USD buying (A)
    # and selling (S) rates for consistency. Hardcoded because the EVDS3 catalog API doesn't expose a clean list
    # and the series rarely change.
    # Browse: https://evds3.tcmb.gov.tr > Exchange Rates > Indicative Exchange Rates
    SERIES = {
      "AED" => "TP.DK.AED.C.YTL",
      "AUD" => "TP.DK.AUD.C.YTL",
      "AZN" => "TP.DK.AZN.C.YTL",
      "CAD" => "TP.DK.CAD.C.YTL",
      "CHF" => "TP.DK.CHF.C.YTL",
      "CNY" => "TP.DK.CNY.C.YTL",
      "DKK" => "TP.DK.DKK.C.YTL",
      "EUR" => "TP.DK.EUR.C.YTL",
      "GBP" => "TP.DK.GBP.C.YTL",
      "JPY" => "TP.DK.JPY.C.YTL",
      "KRW" => "TP.DK.KRW.C.YTL",
      "KWD" => "TP.DK.KWD.C.YTL",
      "KZT" => "TP.DK.KZT.C.YTL",
      "NOK" => "TP.DK.NOK.C.YTL",
      "PKR" => "TP.DK.PKR.C.YTL",
      "QAR" => "TP.DK.QAR.C.YTL",
      "RON" => "TP.DK.RON.C.YTL",
      "RUB" => "TP.DK.RUB.C.YTL",
      "SAR" => "TP.DK.SAR.C.YTL",
      "SEK" => "TP.DK.SEK.C.YTL",
      "TRY_BUY" => "TP.DK.USD.A.YTL",
      "TRY_SELL" => "TP.DK.USD.S.YTL",
    }.freeze

    COLUMNS = SERIES.to_h { |code, series| [series.tr(".", "_"), code] }.freeze

    def key = "TCMB"
    def name = "Central Bank of Turkey"
    def base = "USD"

    def current
      return no_key unless api_key

      start_date = Date.today - 7
      records = fetch(start_date, Date.today)
      last_date = records.last&.dig(:date)
      @dataset = records.select { |r| r[:date] == last_date }
      self
    end

    def historical(start_date: EARLIEST_DATE, end_date: Date.today)
      return no_key unless api_key

      start_date = Date.parse(start_date.to_s)
      end_date = Date.parse(end_date.to_s)
      @dataset = []

      each_chunk(start_date, end_date) do |chunk_start, chunk_end|
        @dataset.concat(fetch(chunk_start, chunk_end))
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

    def fetch(start_date, end_date)
      url = URI("#{EVDS_URL}/series=#{SERIES.values.join("-")}" \
        "&startDate=#{start_date.strftime("%d-%m-%Y")}" \
        "&endDate=#{end_date.strftime("%d-%m-%Y")}" \
        "&type=json&frequency=1")

      response = Net::HTTP.get(url, "key" => api_key)
      data = JSON.parse(response)
      items = data["items"] || []

      items.flat_map do |item|
        date = Date.strptime(item["Tarih"], "%d-%m-%Y")
        rates = {}

        COLUMNS.each do |column, code|
          value = item[column]
          next if value.nil?

          rates[code] = Float(value)
        end

        # Mid of buying and selling for TRY
        buy = rates.delete("TRY_BUY")
        sell = rates.delete("TRY_SELL")
        rates["TRY"] = ((buy + sell) / 2).round(4) if buy && sell

        rates.map do |quote, rate|
          { provider: key, date:, base:, quote:, rate: }
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
