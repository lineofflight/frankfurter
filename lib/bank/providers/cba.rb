# frozen_string_literal: true

require "date"
require "net/http"
require "ox"

require "bank/provider"

module Bank
  module Providers
    class CBA < Provider
      EARLIEST_DATE = Date.new(1999, 1, 4)
      CHUNK_SIZE = 365
      URL = URI("https://api.cba.am/exchangerates.asmx")

      def current
        result = latest_quote
        return [] unless result

        [result]
      end

      def ninety_days
        range(Date.today - 120, Date.today)
      end

      def historical
        chunked_range(EARLIEST_DATE, Date.today)
      end

      def saved_data
        []
      end

      def supported_currencies
        ["AMD"]
      end

      private

      def latest_quote
        response = request("ExchangeRatesLatest", <<~XML)
          <ExchangeRatesLatest xmlns="http://www.cba.am/" />
        XML

        result = response.locate("soap:Envelope/soap:Body/ExchangeRatesLatestResponse/ExchangeRatesLatestResult").first
        return unless result

        current_date = result.locate("CurrentDate").first
        eur = result.locate("Rates/ExchangeRate").find { |node| node.locate("ISO").first&.text == "EUR" }
        return unless current_date && eur

        {
          date: Date.parse(current_date.text),
          rates: { "AMD" => extract_rate(eur) },
        }
      end

      def chunked_range(start_date, end_date)
        days = []
        chunk_start = start_date

        while chunk_start <= end_date
          chunk_end = [chunk_start + CHUNK_SIZE - 1, end_date].min
          days.concat(range(chunk_start, chunk_end))
          chunk_start = chunk_end + 1
        end

        days
      end

      def range(start_date, end_date)
        response = request("ExchangeRatesByDateRangeByISO", <<~XML)
          <ExchangeRatesByDateRangeByISO xmlns="http://www.cba.am/">
            <ISOCodes>EUR</ISOCodes>
            <DateFrom>#{start_date}</DateFrom>
            <DateTo>#{end_date}</DateTo>
          </ExchangeRatesByDateRangeByISO>
        XML

        response
          .locate("soap:Envelope/soap:Body/ExchangeRatesByDateRangeByISOResponse/ExchangeRatesByDateRangeByISOResult/diffgr:diffgram/DocumentElement/ExchangeRatesByRange")
          .map do |row|
            {
              date: Date.parse(row.locate("RateDate").first.text),
              rates: { "AMD" => extract_rate(row) },
            }
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
end
