# frozen_string_literal: true

require "json"
require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banco Central de Cuba. Publishes daily exchange rates against CUP for 13
    # currencies via a JSON REST API.
    #
    # The bank publishes three parallel series:
    #   * tasaOficial — Segment I, official rate (USD pegged at 24 CUP)
    #   * tasaPublica — Segment II, retail bank rate (USD = 120 CUP)
    #   * tasaEspecial — Segment III, informal/MLC market rate, the only float
    #
    # We relay tasaEspecial as the headline series. It tracks the de facto
    # parallel market and is the only one that moves day to day. The other two
    # series are administered pegs published for legal/accounting purposes; we
    # don't emit them.
    #
    # The /historico endpoint accepts arbitrarily wide date ranges in a single
    # request but only one currency at a time (codigoMoneda is required), so
    # we iterate the 13 currencies and hit the endpoint once each per backfill
    # window.
    #
    # Native convention: 1 foreign = X CUP. Base = foreign, quote = CUP. JPY
    # is reported per-unit (e.g. 0.31 CUP per 1 JPY), no multiplier needed.
    class BCC < Adapter
      BASE_URL = "https://api.bc.gob.cu/v1/tasas-de-cambio"
      HISTORICO_URL = "#{BASE_URL}/historico"

      CURRENCIES = ["AUD", "CAD", "CHF", "CNY", "DKK", "EUR", "GBP", "JPY", "MXN", "NOK", "RUB", "SEK", "USD"].freeze

      def fetch(after: nil, upto: nil)
        start_date = after || Date.new(2025, 12, 19)
        end_date = upto || Date.today
        return [] if start_date > end_date

        dataset = []
        first = true
        CURRENCIES.each do |code|
          sleep(1) unless first
          first = false
          dataset.concat(fetch_currency(code, start_date, end_date))
        end
        dataset
      end

      def parse(json, code)
        data = json.is_a?(String) ? JSON.parse(json) : json
        return [] unless data.is_a?(Array)

        data.filter_map do |entry|
          rate = entry["tasaEspecial"]
          next if rate.nil?

          rate = Float(rate)
          next if rate.zero?

          { date: Date.parse(entry["fecha"]), base: code, quote: "CUP", rate: rate }
        end
      end

      private

      def fetch_currency(code, start_date, end_date)
        url = URI(HISTORICO_URL)
        url.query = URI.encode_www_form(
          "fechaInicio" => start_date.to_s,
          "fechaFin" => end_date.to_s,
          "codigoMoneda" => code,
        )
        response = Net::HTTP.get(url)
        parse(response, code)
      end
    end
  end
end
