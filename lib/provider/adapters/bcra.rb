# frozen_string_literal: true

require "json"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Central Bank of Argentina (Banco Central de la Republica Argentina).
    # Fetches official exchange rates in ARS. The API only accepts a single date
    # per request, so we iterate day by day skipping weekends.
    class BCRA < Adapter
      BASE_URL = "https://api.bcra.gob.ar/estadisticascambiarias/v1.0/Cotizaciones"
      # Codes to skip: self-reference, internal reference, defunct currencies, duplicate codes
      SKIP_CODES = ["ARS", "REF", "VEB", "MXP"].freeze

      class << self
        def backfill_range = 90
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        dataset = []

        first = true
        after.upto(end_date) do |date|
          next if date.saturday? || date.sunday?

          sleep(0.5) unless first
          first = false

          dataset.concat(fetch_date(date))
        end

        dataset
      end

      def parse(json)
        data = json.is_a?(String) ? JSON.parse(json) : json
        results = data["results"]
        raise "BCRA: results envelope missing from response" unless results.is_a?(Hash)

        detalle = results["detalle"]
        raise "BCRA: detalle missing from results envelope" unless detalle.is_a?(Array)

        fecha = results["fecha"]
        unless fecha
          # Holidays return {"results":{"fecha":null,"detalle":[]}} with HTTP 200
          return [] if results.key?("fecha") && detalle.empty?

          raise "BCRA: undated results that do not match the holiday shape"
        end

        # Skip the entire date if any currency code appears more than once
        codes = detalle.filter_map { |item| item["codigoMoneda"]&.strip }
        return [] if codes.size != codes.uniq.size

        date = Date.parse(fecha)

        detalle.filter_map do |item|
          code = item["codigoMoneda"]&.strip
          next if code.nil? || SKIP_CODES.include?(code)

          rate_value = Float(item["tipoCotizacion"])
          next if rate_value.zero?

          multiplier = extract_multiplier(item["descripcion"])
          rate_value /= multiplier if multiplier > 1

          { date:, base: code, quote: "ARS", rate: rate_value }
        end
      end

      private

      # BCRA quotes some low-value currencies per N units, indicated in the
      # descripcion field, e.g. "DONG VIETNAM (C/1.000 UNIDADES)".
      # Extract the multiplier so we can normalize to per-1-unit rates.
      def extract_multiplier(descripcion)
        return 1 unless descripcion

        match = descripcion.match(%r{C/([\d.]+)\s*UNIDADES}i)
        return 1 unless match

        # BCRA uses period as thousands separator (e.g. "1.000" = 1000)
        Integer(match[1].delete("."))
      end

      def fetch_date(date)
        response = http.get(BASE_URL, params: { fecha: date.strftime("%Y-%m-%d") }).to_s
        parse(response)
      end
    end
  end
end
