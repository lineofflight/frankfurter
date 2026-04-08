# frozen_string_literal: true

require "net/http"
require "ox"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Magyar Nemzeti Bank. Publishes daily exchange rates for 30+ currencies
    # against the Hungarian forint (HUF) via a SOAP/XML web service.
    # Rates use unit multipliers (e.g. 100 JPY = X HUF) and Hungarian decimal format (comma).
    # SOAP endpoint: http://www.mnb.hu/arfolyamok.asmx
    # WSDL: http://www.mnb.hu/arfolyamok.asmx?WSDL
    class MNB < Adapter
      ENDPOINT = URI("http://www.mnb.hu/arfolyamok.asmx")

      # Active currencies as of 2026. The GetExchangeRates operation requires
      # explicit currency names — an empty list returns empty days.
      CURRENCIES = "AUD,BRL,CAD,CHF,CNY,CZK,DKK,EUR,GBP,HKD,IDR,ILS,INR,ISK,JPY,KRW," \
        "MXN,MYR,NOK,NZD,PHP,PLN,RON,RSD,RUB,SEK,SGD,THB,TRY,UAH,USD,ZAR"

      CHUNK_DAYS = 365

      class << self
        def backfill_range = CHUNK_DAYS
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        xml = fetch_rates(after, end_date)
        parse(xml)
      end

      def parse(xml)
        doc = Ox.load(xml)
        doc.locate("Day").flat_map do |day|
          date = Date.parse(day[:date])
          day.locate("Rate").filter_map do |rate_node|
            currency = rate_node[:curr]
            next unless currency&.match?(/\A[A-Z]{3}\z/)

            unit = rate_node[:unit].to_i
            next if unit.zero?

            value = Float(rate_node.text.tr(",", "."))
            next if value.zero?

            rate = value / unit

            { date:, base: currency, quote: "HUF", rate: }
          end
        end
      end

      private

      def fetch_rates(start_date, end_date)
        body = soap_envelope("GetExchangeRates") do
          <<~XML.strip
            <web:startDate>#{start_date}</web:startDate>
            <web:endDate>#{end_date}</web:endDate>
            <web:currencyNames>#{CURRENCIES}</web:currencyNames>
          XML
        end

        response = post(body, "GetExchangeRates")
        extract_result(response, "GetExchangeRatesResult")
      end

      def soap_envelope(operation)
        <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"
                         xmlns:web="http://www.mnb.hu/webservices/">
            <soap:Body>
              <web:#{operation}>
                #{yield}
              </web:#{operation}>
            </soap:Body>
          </soap:Envelope>
        XML
      end

      def post(body, operation)
        http = Net::HTTP.new(ENDPOINT.host, ENDPOINT.port)
        req = Net::HTTP::Post.new(ENDPOINT.path)
        req["Content-Type"] = "text/xml; charset=utf-8"
        req["SOAPAction"] = "http://www.mnb.hu/webservices/MNBArfolyamServiceSoap/#{operation}"
        req.body = body
        http.request(req)
      end

      def extract_result(response, tag)
        doc = Ox.load(response.body)
        node = doc.locate("*/#{tag}").first
        node&.text || ""
      end
    end
  end
end
