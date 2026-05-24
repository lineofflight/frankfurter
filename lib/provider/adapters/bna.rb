# frozen_string_literal: true

require "net/http"
require "oj"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banco Nacional de Angola. Publishes daily reference rates for ~70 currencies
    # against the Angolan kwanza (AOA), daily Mon-Fri from 2000-01-01 onwards.
    #
    # The time-series endpoint accepts a single currency per request, so we iterate
    # the currency list and fetch each one over the requested window. The list and
    # rate endpoints both live under /service/rest/taxas. Plain HTTPS, no auth,
    # no observed rate limiting.
    #
    # Query params must be lowercase (`datainicio`, `datafim`, `tipocambio`,
    # `moeda`). Mixed case silently returns `datainicio 'null' inválida.`. The
    # response is JSON `{ genericResponse: [...], success: bool }`. Each rate row
    # carries `tipoCambio` in {B=venda/sell, G=compra/buy, M=medio/mid}; we filter
    # to mid via `tipocambio=M`.
    #
    # XDRUSD is a non-ISO composite the API includes alongside real currencies —
    # excluded here. XAU is excluded too: BNA's series has documented unit
    # inconsistencies (mid-2024 rows alternate between AOA-per-ounce and
    # USD-per-ounce in the same column), so faithfully relaying it would emit
    # bad data.
    #
    # Rates are published in BNA's native direction — `1 foreign = X AOA` — so
    # foreign currency is stored as `base` and AOA as `quote`, matching the
    # NBG/BBK pivot-in-quote convention.
    class BNA < Adapter
      BASE_URL = "https://www.bna.ao/service/rest/taxas"
      LIST_PATH = "/get/lista/moedas"
      SERIES_PATH = "/get/evolucao/taxa/intervalo"

      # Non-ISO composite series we ignore. BNA still publishes other historical
      # codes (EEK, HRK, STD, etc.); those are valid ISO 4217 codes recognised by
      # Money::Currency, so Provider#backfill's default filter passes them through.
      EXCLUDED_CODES = ["XDRUSD", "XAU"].freeze

      def fetch(after: nil, upto: nil)
        start_date = after || Date.new(2000, 1, 1)
        end_date = upto || Date.today
        return [] if start_date > end_date

        codes = currency_codes
        dataset = []

        codes.each_with_index do |code, idx|
          sleep(0.2) if idx.nonzero?
          dataset.concat(fetch_currency(code, start_date, end_date))
        end

        dataset
      end

      def parse(json)
        data = json.is_a?(String) ? Oj.load(json, mode: :strict) : json
        return [] unless data.is_a?(Hash) && data["success"] && data["genericResponse"].is_a?(Array)

        data["genericResponse"].filter_map do |row|
          next unless row["tipoCambio"] == "M"

          code = row["codigoMoeda"]
          next unless code&.match?(/\A[A-Z]{3}\z/)

          rate = Float(row["taxa"], exception: false)
          next if rate.nil? || rate.zero?

          { date: Date.parse(row["data"]), base: code, quote: "AOA", rate: rate }
        end
      end

      private

      def currency_codes
        json = Net::HTTP.get(URI("#{BASE_URL}#{LIST_PATH}"))
        data = Oj.load(json, mode: :strict)
        return [] unless data.is_a?(Hash) && data["genericResponse"].is_a?(Array)

        data["genericResponse"].filter_map do |row|
          code = row["codigoMoeda"]
          next unless code&.match?(/\A[A-Z]{3,6}\z/)
          next if code == "AOA"
          next if EXCLUDED_CODES.include?(code)

          code
        end
      end

      def fetch_currency(code, start_date, end_date)
        url = URI("#{BASE_URL}#{SERIES_PATH}")
        url.query = URI.encode_www_form(
          datainicio: start_date.to_s,
          datafim: end_date.to_s,
          tipocambio: "M",
          moeda: code,
        )
        parse(Net::HTTP.get(url))
      end
    end
  end
end
