# frozen_string_literal: true

require "json"
require "net/http"

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
        detalle = data.dig("results", "detalle")
        fecha = data.dig("results", "fecha")
        return [] unless detalle && fecha

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
        url = URI("#{BASE_URL}?fecha=#{date.strftime("%Y-%m-%d")}")
        response = Net::HTTP.get(url)
        parse(response)
      end
    end
  end
end
