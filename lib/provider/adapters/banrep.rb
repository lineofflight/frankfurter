# frozen_string_literal: true

require "json"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banco de la República Colombia. Publishes the daily TRM (Tasa
    # Representativa del Mercado) — the representative market exchange rate
    # for the US dollar against the Colombian peso — via the Socrata Open
    # Data API on datos.gov.co.
    class BANREP < Adapter
      BASE_URL = "https://www.datos.gov.co/resource/32sa-8pi3.json"
      def fetch(after: nil, upto: nil)
        response = http.get(BASE_URL, params: {
          "$where" => "vigenciadesde>='#{after}T00:00:00.000' AND vigenciadesde<='#{upto || Date.today}T00:00:00.000'",
          "$limit" => 50_000,
          "$order" => "vigenciadesde ASC",
        }).to_s

        parse(response)
      end

      def parse(json)
        records = json.is_a?(String) ? JSON.parse(json) : json
        raise "BANREP: expected JSON array from #{BASE_URL}, got #{records.class}" unless records.is_a?(Array)

        records.filter_map do |record|
          date_str = record["vigenciadesde"]
          value_str = record["valor"]
          next if date_str.nil? || value_str.nil?

          date = Date.parse(date_str)
          rate_value = Float(value_str)
          next if rate_value.zero?

          { date:, base: "USD", quote: "COP", rate: rate_value }
        end
      end
    end
  end
end
