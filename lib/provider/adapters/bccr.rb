# frozen_string_literal: true

require "json"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banco Central de Costa Rica (BCCR). Fetches the daily reference exchange rate for the US dollar against the Costa
    # Rican colón via the SDDE site's CSV download, which returns any date range in one request. The table endpoint the
    # site renders from caps each request at five dates.
    class BCCR < Adapter
      BASE_URL = "https://apim.bccr.fi.cr/SDDE/api"
      TOKEN_URL = "#{BASE_URL}/Bccr.GE.SDDE.IndicadoresSitioExterno.ServiciosUsuario.API/Token/GenereCSRF".freeze
      DATA_URL = "#{BASE_URL}/Bccr.GE.SDDE.IndicadoresSitioExterno.GrupoVariables.API/Documento/DescargueCsv".freeze
      # IdGrupoVariable=1 is the USD buy/sell reference rate group
      GROUP_ID = 1

      # Indicator 318 is the sell (venta) rate: CRC per 1 USD
      SELL_INDICATOR = 318

      HEADER = "Date;Sales Exchange Rate"

      def fetch(after: nil, upto: nil)
        token = fetch_token

        response = http.headers("token_csrf" => token, "Origin" => "https://sdd.bccr.fi.cr").post(DATA_URL, json: {
          "FechaInicio" => after.to_s,
          "FechaFin" => (upto || Date.today).to_s,
          "Periodicidad" => 0,
          "IdGrupoVariable" => GROUP_ID,
          "Idioma" => "EN",
          "Indicadores" => [SELL_INDICATOR],
        },).to_s

        # The CSV comes base64-encoded in a JSON envelope; estado is false when the range holds no rates.
        data = JSON.parse(response)
        raise "BCCR: no data between #{after} and #{upto || Date.today}" unless data["estado"]

        parse(data["datos"].unpack1("m").force_encoding(Encoding::UTF_8))
      end

      def parse(csv)
        lines = csv.delete_prefix("﻿").lines(chomp: true)
        header = lines.index(HEADER)
        raise "BCCR: CSV missing #{HEADER.inspect} header" unless header

        lines.drop(header + 1).filter_map do |line|
          date_str, value_str = line.split(";")
          next if value_str.nil? || value_str.empty?

          rate_value = Float(value_str)
          next if rate_value.zero?

          { date: Date.iso8601(date_str), base: "USD", quote: "CRC", rate: rate_value }
        end
      end

      private

      def fetch_token
        http.get(TOKEN_URL).to_s
      end
    end
  end
end
