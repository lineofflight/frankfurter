# frozen_string_literal: true

require "json"
require "net/http"

require "providers/base"

module Providers
  # Central Bank of Argentina (Banco Central de la Republica Argentina).
  # Fetches official exchange rates in ARS. The API only accepts a single date
  # per request, so we iterate day by day skipping weekends.
  class BCRA < Base
    BASE_URL = "https://api.bcra.gob.ar/estadisticascambiarias/v1.0/Cotizaciones"
    EARLIEST_DATE = Date.new(2000, 1, 3)

    # Codes to skip: self-reference, internal reference, defunct currencies, duplicate codes
    SKIP_CODES = ["ARS", "REF", "VEB", "MXP"].freeze

    class << self
      def key = "BCRA"
      def name = "Central Bank of Argentina"
      def earliest_date = EARLIEST_DATE

      def backfill(range: 90)
        super
      end
    end

    def fetch(since: nil, upto: nil)
      start_date = since || EARLIEST_DATE
      start_date = Date.parse(start_date.to_s)
      end_date = upto || Date.today
      end_date = Date.parse(end_date.to_s)
      @dataset = []

      first = true
      start_date.upto(end_date) do |date|
        next if date.saturday? || date.sunday?

        sleep(0.5) unless first
        first = false

        @dataset.concat(fetch_date(date))
      end

      self
    end

    def parse(json)
      data = json.is_a?(String) ? JSON.parse(json) : json
      detalle = data.dig("results", "detalle")
      return [] unless detalle

      date = Date.parse(data.dig("results", "fecha"))

      detalle.filter_map do |item|
        code = item["codigoMoneda"]&.strip
        next if code.nil? || SKIP_CODES.include?(code)

        rate_value = Float(item["tipoCotizacion"])
        next if rate_value.zero?

        { provider: key, date:, base: code, quote: "ARS", rate: rate_value }
      rescue ArgumentError, TypeError
        nil
      end
    end

    private

    def fetch_date(date)
      url = URI("#{BASE_URL}?fecha=#{date.strftime("%Y-%m-%d")}")
      response = Net::HTTP.get(url)
      parse(response)
    rescue JSON::ParserError, Net::OpenTimeout, Net::ReadTimeout
      []
    end
  end
end
