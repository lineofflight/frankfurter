# frozen_string_literal: true

require "net/http"
require "ox"

require "providers/base"

module Providers
  # Central Bank of Uruguay (Banco Central del Uruguay).
  # Fetches official exchange rates in UYU via SOAP web service.
  # Rates are stored with base=quote_currency, quote=UYU.
  class BCU < Base
    ENDPOINT = URI("https://cotizaciones.bcu.gub.uy/wscotizaciones/servlet/awsbcucotizaciones")
    EARLIEST_DATE = Date.new(2000, 1, 1)

    # Map BCU numeric currency codes to ISO 4217 codes
    CURRENCY_MAPPING = {
      "2225" => "USD",  # DLS. USA BILLETE
      "1111" => "EUR",  # EURO
      "1000" => "BRL",  # REAL
      "0500" => "ARS",  # PESO ARGENTINO
      "0501" => "ARS",  # PESO ARG. BILLETE
      "1001" => "BRL",  # REAL BILLETE
      "2309" => "CAD",  # DOLAR CANADIENSE
      "1300" => "CLP",  # PESO CHILENO
      "4150" => "CNY",  # YUAN RENMIMBI
      "5500" => "COP",  # PESO COLOMBIANO
      "1800" => "DKK",  # CORONA DANESA
      "5100" => "HKD",  # DOLAR HONG KONG
      "4300" => "HUF",  # FORINT HUNGARO
      "5700" => "INR",  # RUPIA INDIA
      "2700" => "GBP",  # LIBRA ESTERLINA
      "4900" => "ISK",  # CORONA ISLANDESA
      "3600" => "JPY",  # YEN
      "5300" => "KRW",  # WON
      "5600" => "MYR",  # RINGGIT
      "4200" => "MXN",  # PESO MEXICANO
      "4600" => "NOK",  # CORONA NORUEGA
      "1490" => "NZD",  # DOL. NEOZELANDES
      "4800" => "PYG",  # GUARANI
      "4000" => "PEN",  # NVO.SOL PERUANO
      "5400" => "RUB",  # RUBLO
      "1620" => "ZAR",  # RAND SUDAFRICANO
      "5800" => "SEK",  # CORONA SUECA
      "5900" => "CHF",  # FRANCO SUIZO
      "4400" => "TRY",  # LIRA TURCA
    }.freeze

    class << self
      def key = "BCU"
      def name = "Central Bank of Uruguay"
      def earliest_date = EARLIEST_DATE

      def backfill(range: 365)
        super
      end
    end

    def fetch(since: nil, upto: nil)
      start_date = since || EARLIEST_DATE
      end_date = upto || Date.today

      @dataset = []
      currencies = CURRENCY_MAPPING.values.uniq
      currencies.each_with_index do |iso_code, index|
        body = soap_request(iso_code, start_date, end_date)
        response = Net::HTTP.start(ENDPOINT.host, ENDPOINT.port, use_ssl: true) do |http|
          req = Net::HTTP::Post.new(ENDPOINT)
          req["Content-Type"] = "text/xml; charset=utf-8"
          req.body = body
          http.request(req)
        end
        @dataset.concat(parse(response.body, iso_code))
        sleep(0.1) unless index == currencies.size - 1  # Be polite to the API, but not after last request
      end

      self
    rescue Net::OpenTimeout, Net::ReadTimeout, OpenSSL::SSL::SSLError
      @dataset = []
      self
    end

    def parse(xml, iso_code)
      doc = Ox.load(xml)
      doc.locate("*/datoscotizaciones.dato").filter_map do |dato|
        date_str = dato.locate("Fecha").first&.text
        moneda_str = dato.locate("Moneda").first&.text
        tcc_str = dato.locate("TCC").first&.text
        tcv_str = dato.locate("TCV").first&.text

        next unless date_str && moneda_str && tcc_str && tcv_str

        begin
          # Map the currency code to ISO
          base_currency = CURRENCY_MAPPING[moneda_str]
          next unless base_currency

          # Skip UYU self-reference
          next if base_currency == "UYU"

          date = Date.parse(date_str)
          tcc = Float(tcc_str)
          tcv = Float(tcv_str)
          next if tcc.zero? || tcv.zero?

          # Calculate midpoint rate
          rate = (tcc + tcv) / 2.0

          { provider: key, date:, base: base_currency, quote: "UYU", rate: }
        rescue ArgumentError, TypeError
          nil
        end
      end
    rescue Ox::ParseError, ArgumentError, TypeError
      []
    end

    private

    def soap_request(iso_code, start_date, end_date)
      currency_code = CURRENCY_MAPPING.key(iso_code)
      return "" unless currency_code

      <<~XML
        <?xml version="1.0" encoding="UTF-8"?>
        <soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"
                           xmlns:cot="Cotiza">
           <soapenv:Header />
           <soapenv:Body>
              <cot:wsbcucotizaciones.Execute>
                 <cot:Entrada>
                    <cot:Moneda>
                       <cot:item>#{currency_code}</cot:item>
                    </cot:Moneda>
                    <cot:FechaDesde>#{start_date.strftime("%Y-%m-%d")}</cot:FechaDesde>
                    <cot:FechaHasta>#{end_date.strftime("%Y-%m-%d")}</cot:FechaHasta>
                    <cot:Grupo>0</cot:Grupo>
                 </cot:Entrada>
              </cot:wsbcucotizaciones.Execute>
           </soapenv:Body>
        </soapenv:Envelope>
      XML
    end
  end
end
