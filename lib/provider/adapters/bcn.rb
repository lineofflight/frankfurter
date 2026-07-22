# frozen_string_literal: true

require "ox"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banco Central de Nicaragua. Publishes the daily official exchange rate
    # for the US dollar against the Nicaraguan córdoba via a SOAP web service.
    # The RecuperaTC_Mes method returns all daily rates for a given month.
    class BCN < Adapter
      ENDPOINT = URI("https://servicios.bcn.gob.ni/Tc_Servicio/ServicioTC.asmx")
      SOAP_ACTION = '"http://servicios.bcn.gob.ni/RecuperaTC_Mes"'

      class << self
        def backfill_range = 30
      end

      def fetch(after: nil, upto: nil)
        check_tls_support!
        end_date = upto || Date.today
        dataset = []

        current = Date.new(after.year, after.month, 1)
        first = true
        while current <= end_date
          sleep(0.2) unless first
          first = false

          response = fetch_month(current.year, current.month)
          records = parse(response)
          dataset.concat(records.select { |r| r[:date].between?(after, end_date) })

          current = current.next_month
        end

        dataset
      end

      def parse(xml)
        xml = Ox.load(xml) if xml.is_a?(String)

        xml.locate("*/Tc").filter_map do |tc|
          date_str = tc.locate("Fecha").first&.text
          value_str = tc.locate("Valor").first&.text
          next unless date_str && value_str

          date = Date.parse(date_str)
          rate = Float(value_str)
          next if rate.zero?

          { date:, base: "USD", quote: "NIO", rate: }
        end
      end

      private

      def fetch_month(year, month)
        post_envelope(soap_envelope(year, month))
      end

      def post_envelope(envelope)
        http
          .headers("Content-Type" => "text/xml; charset=utf-8", "SOAPAction" => SOAP_ACTION)
          .post(ENDPOINT, body: envelope, ssl_context: legacy_tls_context)
          .to_s
      end

      def legacy_tls_context
        OpenSSL::SSL::SSLContext.new.tap do |ctx|
          # set_params restores VERIFY_PEER and hostname verification, which a bare context omits.
          ctx.set_params
          ctx.min_version = OpenSSL::SSL::TLS1_VERSION
          ctx.security_level = 0
        end
      end

      def check_tls_support!
        ctx = OpenSSL::SSL::SSLContext.new
        raise "legacy TLS required" if ctx.security_level > 0
      end

      def soap_envelope(year, month)
        <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"
                         xmlns:tns="http://servicios.bcn.gob.ni/">
            <soap:Body>
              <tns:RecuperaTC_Mes>
                <tns:Ano>#{year}</tns:Ano>
                <tns:Mes>#{month}</tns:Mes>
              </tns:RecuperaTC_Mes>
            </soap:Body>
          </soap:Envelope>
        XML
      end
    end
  end
end
