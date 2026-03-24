# frozen_string_literal: true

require "net/http"
require "ox"

require "providers/base"

module Providers
  # Banco de Guatemala. Publishes daily reference exchange rates for GTQ/USD
  # via a SOAP web service. The rate is GTQ per 1 USD, so base=USD, quote=GTQ.
  class Banguat < Base
    ENDPOINT = URI("https://www.banguat.gob.gt/variables/ws/TipoCambio.asmx")
    EARLIEST_DATE = Date.new(2000, 1, 1)

    class << self
      def key = "BANGUAT"
      def name = "Banco de Guatemala"
      def earliest_date = EARLIEST_DATE

      def backfill(range: 365)
        super
      end
    end

    def fetch(since: nil, upto: nil)
      start_date = since || EARLIEST_DATE
      end_date = upto || Date.today

      body = soap_request(start_date, end_date)
      response = Net::HTTP.start(ENDPOINT.host, ENDPOINT.port, use_ssl: true) do |http|
        req = Net::HTTP::Post.new(ENDPOINT)
        req["Content-Type"] = "text/xml; charset=utf-8"
        req.body = body
        http.request(req)
      end

      @dataset = parse(response.body)
      self
    rescue Net::OpenTimeout, Net::ReadTimeout, OpenSSL::SSL::SSLError
      @dataset = []
      self
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

        { provider: key, date:, base: "USD", quote: "GTQ", rate: }
      rescue ArgumentError, TypeError
        nil
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
