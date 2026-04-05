# frozen_string_literal: true

require "net/http"
require "ox"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banco de Guatemala. Publishes daily reference exchange rates for GTQ/USD
    # via a SOAP web service. The rate is GTQ per 1 USD, so base=USD, quote=GTQ.
    # NOTE: Currently disabled — the SOAP endpoint rejects connections from
    # certain datacenter IP ranges during TLS handshake.
    class Banguat < Adapter
      ENDPOINT = URI("https://www.banguat.gob.gt/variables/ws/TipoCambio.asmx")
      class << self
        def backfill_range = 365
      end

      def fetch(after: nil, upto: nil)
        body = soap_request(after, upto || Date.today)
        response = Net::HTTP.start(ENDPOINT.host, ENDPOINT.port, use_ssl: true) do |http|
          req = Net::HTTP::Post.new(ENDPOINT)
          req["Content-Type"] = "text/xml; charset=utf-8"
          req.body = body
          http.request(req)
        end

        @dataset = parse(response.body)
      end

      def parse(xml)
        doc = Ox.load(xml)
        doc.locate("*/Var").filter_map do |var|
          date_str = var.locate("fecha").first&.text
          rate_str = var.locate("venta").first&.text
          next unless date_str && rate_str

          date = Date.strptime(date_str, "%d/%m/%Y")
          rate = Float(rate_str)
          next if rate.zero?

          { date:, base: "USD", quote: "GTQ", rate: }
        end
      end

      private

      def soap_request(start_date, end_date)
        <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
            <soap:Body>
              <TipoCambioRango xmlns="http://www.banguat.gob.gt/variables/ws/">
                <fechainit>#{start_date.strftime("%d/%m/%Y")}</fechainit>
                <fechafin>#{end_date.strftime("%d/%m/%Y")}</fechafin>
              </TipoCambioRango>
            </soap:Body>
          </soap:Envelope>
        XML
      end
    end
  end
end
