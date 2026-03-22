# frozen_string_literal: true

require "net/http"
require "ox"

require "providers/base"

module Providers
  # Central Bank of Armenia. Publishes daily rates for ~30 currencies against AMD.
  class CBA < Base
    URL = URI("https://api.cba.am/exchangerates.asmx")
    EARLIEST_DATE = Date.new(1999, 1, 4)
    CHUNK_SIZE = 365

    class << self
      def key = "CBA"
      def name = "Central Bank of Armenia"
    end

    def fetch(since: nil)
      start_date = since || EARLIEST_DATE
      start_date = Date.parse(start_date.to_s)
      @dataset = chunked_range(start_date, Date.today, currency_codes)
      self
    end

    private

    def latest_rates
      response = request("ExchangeRatesLatest", <<~XML)
        <ExchangeRatesLatest xmlns="http://www.cba.am/" />
      XML

      result = response.locate("soap:Envelope/soap:Body/ExchangeRatesLatestResponse/ExchangeRatesLatestResult").first
      return [] unless result

      current_date = result.locate("CurrentDate").first
      return [] unless current_date

      date = Date.parse(current_date.text)
      result.locate("Rates/ExchangeRate").filter_map do |node|
        iso = node.locate("ISO").first&.text
        next unless iso

        { provider: key, date:, base: iso, quote: "AMD", rate: extract_rate(node) }
      end
    end

    def currency_codes
      latest_rates.map { |r| r[:base] }.join(",")
    end

    def chunked_range(start_date, end_date, iso_codes)
      records = []
      chunk_start = start_date

      while chunk_start <= end_date
        chunk_end = [chunk_start + CHUNK_SIZE - 1, end_date].min
        records.concat(range(chunk_start, chunk_end, iso_codes))
        chunk_start = chunk_end + 1
      end

      records
    end

    def range(start_date, end_date, iso_codes)
      response = request("ExchangeRatesByDateRangeByISO", <<~XML)
        <ExchangeRatesByDateRangeByISO xmlns="http://www.cba.am/">
          <ISOCodes>#{iso_codes}</ISOCodes>
          <DateFrom>#{start_date}</DateFrom>
          <DateTo>#{end_date}</DateTo>
        </ExchangeRatesByDateRangeByISO>
      XML

      response
        .locate("soap:Envelope/soap:Body/ExchangeRatesByDateRangeByISOResponse/ExchangeRatesByDateRangeByISOResult/diffgr:diffgram/DocumentElement/ExchangeRatesByRange")
        .filter_map do |row|
          iso = row.locate("ISO").first&.text
          next unless iso

          { provider: key, date: Date.parse(row.locate("RateDate").first.text), base: iso, quote: "AMD", rate: extract_rate(row) }
        end
    end

    def request(action, payload)
      xml = <<~XML
        <?xml version="1.0" encoding="utf-8"?>
        <soap:Envelope xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
                       xmlns:xsd="http://www.w3.org/2001/XMLSchema"
                       xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
          <soap:Body>
            #{payload.strip}
          </soap:Body>
        </soap:Envelope>
      XML

      Ox.load(
        Net::HTTP.post(
          URL,
          xml,
          {
            "Content-Type" => "text/xml; charset=utf-8",
            "SOAPAction" => "\"http://www.cba.am/#{action}\"",
          },
        ).body,
      )
    end

    def extract_rate(node)
      amount = Integer(node.locate("Amount").first.text)
      rate = Float(node.locate("Rate").first.text)
      rate / amount
    end
  end
end
