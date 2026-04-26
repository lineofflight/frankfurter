# frozen_string_literal: true

require "json"
require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banco de Portugal — pre-euro daily PTE reference rates from BPstat (JSON-stat 2.0).
    # Coverage 1987-01-02 through 1998-12-31, when PTE was replaced by EUR.
    #
    # Direction: foreign currency in base, PTE in quote (1 foreign = X PTE), matching
    # the convention used by other pivot-in-quote adapters (e.g. NBG, BBK).
    #
    # The dim_cats filter restricts to BdP-authored, PTE-referenced, daily series.
    # Post-1999 EUR-quoted PTE series are sourced from LSEG (redistribution-restricted)
    # and are structurally excluded by source=BdP, not by date. Post-1999 BdP-authored
    # EUR rates would mirror ECB and are also excluded by reference=PTE.
    class BDP < Adapter
      DATASET_URL = "https://bpstat.bportugal.pt/data/v1/domains/29/datasets/" \
        "23e0cdd56bddb4ad3016a9c3ad63a539/"

      # reference=PTE (794), source=Banco de Portugal (35), periodicity=daily (4263).
      # Re-included on every page request: the API's next_page URL drops some of these,
      # which causes the result set to bleed in non-BdP-sourced rows.
      DIM_CATS = ["13:794", "18:35", "40:4263"].freeze

      # Series labels carry the ISO code in parens, e.g. "US, Dollars (USD) against Escudo - daily".
      LABEL_CODE = /\(([A-Z]{3})\)/

      # BdP labels the European Currency Unit as "ECU"; ISO 4217 uses XEU (numeric 954).
      CODE_REMAP = { "ECU" => "XEU" }.freeze

      class << self
        def backfill_range = 1826 # ~5 years per chunk
      end

      def fetch(after: nil, upto: nil)
        upto ||= Date.today
        dataset = []
        page = 1

        loop do
          json = fetch_page(after, upto, page)
          dataset.concat(parse(json))
          break unless json.dig("extension", "next_page")

          page += 1
          sleep(0.2)
        end

        dataset
      end

      def parse(json)
        data = json.is_a?(String) ? JSON.parse(json) : json
        return [] unless data.is_a?(Hash)

        codes = build_codes(data)
        counterparty_index = data.dig("dimension", "12", "category", "index") || []
        date_index = data.dig("dimension", "reference_date", "category", "index") || []
        values = data["value"] || []
        num_dates = date_index.size
        return [] if codes.empty? || num_dates.zero?

        records = []
        counterparty_index.each_with_index do |category_id, i|
          code = codes[category_id]
          next unless code

          date_index.each_with_index do |date_str, j|
            value = values[i * num_dates + j]
            next if value.nil?

            records << { date: Date.parse(date_str), base: code, quote: "PTE", rate: Float(value) }
          end
        end

        records
      end

      private

      def fetch_page(after, upto, page)
        url = URI(DATASET_URL)
        params = [["lang", "EN"], ["page", page]]
        DIM_CATS.each { |dim| params << ["dim_cats", dim] }
        params << ["obs_since", after.to_s] if after
        params << ["obs_to", upto.to_s] if upto
        url.query = URI.encode_www_form(params)

        response = Net::HTTP.get_response(url)
        response.value
        JSON.parse(response.body)
      end

      def build_codes(data)
        series = data.dig("extension", "series") || []
        series.each_with_object({}) do |entry, codes|
          counterparty = entry["dimension_category"]&.find { |c| c["dimension_id"] == 12 }
          next unless counterparty

          match = entry["label"]&.match(LABEL_CODE)
          next unless match

          code = match[1]
          codes[counterparty["category_id"].to_s] = CODE_REMAP.fetch(code, code)
        end
      end
    end
  end
end
