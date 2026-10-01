# frozen_string_literal: true

require "oj"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banco Nacional de Angola. Publishes daily reference rates for ~70 currencies against the Angolan kwanza (AOA),
    # daily Mon-Fri from 2000-01-01 onwards.
    #
    # The time-series endpoint accepts a single currency per request, so we iterate the currency list and fetch each one
    # over the requested window. The list and rate endpoints both live under /service/rest/taxas. Plain HTTPS, no auth,
    # no observed rate limiting.
    #
    # Query params must be lowercase (`datainicio`, `datafim`, `tipocambio`, `moeda`). Mixed case silently returns
    # `datainicio 'null' inválida.`. The response is JSON `{ genericResponse: [...], success: bool }`. Each rate row
    # carries `tipoCambio` in {B=venda/sell, G=compra/buy, M=medio/mid}; we filter to mid via `tipocambio=M`.
    #
    # XDRUSD is a non-ISO composite the API includes alongside real currencies — excluded here. XAU is excluded too:
    # BNA's series has documented unit inconsistencies (mid-2024 rows alternate between AOA-per-ounce and USD-per-ounce
    # in the same column), so faithfully relaying it would emit bad data.
    #
    # Rates are published in BNA's native direction — `1 foreign = X AOA` — so foreign currency is stored as `base` and
    # AOA as `quote`, matching the NBG/BBK pivot-in-quote convention.
    class BNA < Adapter
      BASE_URL = "https://www.bna.ao/service/rest/taxas"
      LIST_PATH = "/get/lista/moedas"
      SERIES_PATH = "/get/evolucao/taxa/intervalo"

      # Composite and gold series we ignore. BNA's historical codes (EEK, HRK, STD, etc.) remain available through
      # provider routes; their terminal dates apply to the blend and catalogue.
      EXCLUDED_CODES = ["XDRUSD", "XAU"].freeze

      # Three of those codes switch to their successor's values without changing label. MZM's 2014 to 2016 rows track
      # the new metical (3.1 AOA). STD holds a frozen old-dobra cross until 2023-02-17 (0.024 AOA) and the new dobra
      # from 2023-02-22 (21.89 AOA). VEF holds the last pre-2018 official rate until October 2022 (0.002 AOA) and the
      # current bolivar from 2023-10-18 (23.74 AOA), against BCV's VES.
      SUCCESSORS = {
        "MZM" => ["MZN", Date.new(2006, 7, 1)],
        "STD" => ["STN", Date.new(2023, 2, 22)],
        "VEF" => ["VES", Date.new(2023, 10, 18)],
      }.freeze

      def fetch(after: nil, upto: nil)
        start_date = after || Date.new(2000, 1, 1)
        end_date = upto || Date.today
        # Window sanity: a caller-supplied range can be empty
        return [] if start_date > end_date

        # Fetch relabelled series last, so a day BNA also publishes under the successor's own code keeps that row when
        # the insert skips the duplicate.
        codes = currency_codes.partition { |code| !SUCCESSORS.key?(code) }.flatten
        dataset = []

        codes.each_with_index do |code, idx|
          sleep(0.2) if idx.nonzero?
          dataset.concat(fetch_currency(code, start_date, end_date))
        end

        dataset
      end

      def parse(json)
        data = json.is_a?(String) ? Oj.load(json, mode: :strict) : json
        unless data.is_a?(Hash) && data["success"] && data["genericResponse"].is_a?(Array)
          raise "BNA: series request failed: #{data.is_a?(Hash) ? data["message"] : data.class}"
        end

        data["genericResponse"].filter_map do |row|
          next unless row["tipoCambio"] == "M"

          code = row["codigoMoeda"]
          next unless code&.match?(/\A[A-Z]{3}\z/)

          rate = Float(row["taxa"], exception: false)
          next if rate.nil? || rate.zero?

          date = Date.parse(row["data"])
          { date:, base: historical_code(code, date), quote: "AOA", rate: rate }
        end
      end

      private

      def currency_codes
        json = http.get("#{BASE_URL}#{LIST_PATH}").to_s
        data = Oj.load(json, mode: :strict)
        unless data.is_a?(Hash) && data["genericResponse"].is_a?(Array)
          raise "BNA: currency list request failed: #{data.is_a?(Hash) ? data["message"] : data.class}"
        end

        data["genericResponse"].filter_map do |row|
          code = row["codigoMoeda"]
          next unless code&.match?(/\A[A-Z]{3,6}\z/)
          next if code == "AOA"
          next if EXCLUDED_CODES.include?(code)

          code
        end
      end

      def fetch_currency(code, start_date, end_date)
        response = http.get("#{BASE_URL}#{SERIES_PATH}", params: {
          datainicio: start_date.to_s,
          datafim: end_date.to_s,
          tipocambio: "M",
          moeda: code,
        },).to_s
        parse(response)
      end
    end
  end
end
