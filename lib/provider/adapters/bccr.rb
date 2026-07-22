# frozen_string_literal: true

require "json"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banco Central de Costa Rica (BCCR). Fetches the daily reference exchange
    # rate for the US dollar against the Costa Rican colón via the SDDE API.
    # The API returns a pivoted table (dates as columns) with a maximum of 100
    # date columns per request, so backfill is chunked in 90-day periods.
    class BCCR < Adapter
      BASE_URL = "https://apim.bccr.fi.cr/SDDE/api"
      TOKEN_URL = "#{BASE_URL}/Bccr.GE.SDDE.IndicadoresSitioExterno.ServiciosUsuario.API/Token/GenereCSRF"
      DATA_URL = "#{BASE_URL}/Bccr.GE.SDDE.IndicadoresSitioExterno.GrupoVariables.API/CuadroGrupoVariables/ObtenerDatosCuadro"
      # IdGrupoVariable=1 is the USD buy/sell reference rate group
      GROUP_ID = 1

      # Indicator 318 is the sell (venta) rate — CRC per 1 USD
      SELL_INDICATOR = 318

      class << self
        def backfill_range = 90
      end

      def fetch(after: nil, upto: nil)
        token = fetch_token

        response = http.headers("token_csrf" => token, "Origin" => "https://sdd.bccr.fi.cr").get(DATA_URL, params: {
          "IdGrupoVariable" => GROUP_ID,
          "FechaInicio" => "#{after}T00:00:00",
          "FechaFin" => (upto || Date.today).to_s,
          "CantidadSeriesAMostrar" => 100,
        }).to_s

        parse(response)
      end

      def parse(json)
        data = json.is_a?(String) ? JSON.parse(json) : json
        columns = data["columnas"]
        indicators = data["indicadoresRaiz"]
        return [] unless columns && indicators

        sell = indicators.find { |i| i["idIndicador"] == SELL_INDICATOR }
        return [] unless sell

        series = sell["series"]

        columns.drop(1).filter_map do |col|
          index = col["field"].delete_prefix("serie")
          value_str = series["serie#{index}Ingles"]
          next if value_str.nil? || value_str.empty?

          date = parse_date(col["tituloIngles"])
          next unless date

          rate_value = Float(value_str)
          next if rate_value.zero?

          { date:, base: "USD", quote: "CRC", rate: rate_value }
        end
      end

      private

      def fetch_token
        http.get(TOKEN_URL).to_s
      end

      def parse_date(date_str)
        Date.parse(date_str)
      end
    end
  end
end
