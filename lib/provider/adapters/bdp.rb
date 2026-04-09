# frozen_string_literal: true

require "net/http"
require "oj"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banco de Portugal. Fetches daily exchange rates for ~24 currencies
    # against the Portuguese escudo (PTE) from the BPstat JSON-stat API.
    # Historical data only, covering 1987-01-02 to 1998-12-31.
    class BDP < Adapter
      BASE_URL = "https://bpstat.bportugal.pt/data/v1/domains/29/datasets/23e0cdd56bddb4ad3016a9c3ad63a539/"

      # Maps BPstat series IDs to ISO 4217 currency codes.
      SERIES = {
        180121 => "USD",
        180122 => "ECU",
        180123 => "GBP",
        180124 => "SEK",
        180125 => "CHF",
        180126 => "ESP",
        180127 => "ZAR",
        180128 => "NOK",
        180129 => "IEP",
        180130 => "ITL",
        180131 => "NLG",
        180132 => "MOP",
        180133 => "JPY",
        180134 => "DKK",
        180135 => "FRF",
        180136 => "DEM",
        180137 => "GRD",
        180138 => "CAD",
        180139 => "FIM",
        180140 => "CVE",
        180141 => "BEF",
        180142 => "ATS",
        180182 => "AUD",
        180183 => "BRL",
      }.freeze

      def fetch(after: nil, upto: nil)
        records = []
        url = build_url
        while url
          data = fetch_page(url)
          records.concat(parse(data))
          url = data.dig("extension", "next_page")
        end
        filter(records, after:, upto:)
      end

      def parse(data)
        dates = data.dig("dimension", "reference_date", "category", "index")
        currency_index = data.dig("dimension", "12", "category", "index")
        series = data.dig("extension", "series") || []
        values = data["value"]
        return [] unless dates && currency_index && values

        # Map category IDs to ISO codes using series metadata
        cat_to_iso = {}
        series.each do |s|
          dims = s["dimension_category"].to_h { |d| [d["dimension_id"], d["category_id"]] }
          cat_id = dims[12].to_s
          iso = SERIES[s["id"]]
          cat_to_iso[cat_id] = iso if iso
        end

        num_dates = dates.length
        records = []
        currency_index.each_with_index do |cat_id, currency_idx|
          iso = cat_to_iso[cat_id]
          next unless iso

          num_dates.times do |date_idx|
            value = values[currency_idx * num_dates + date_idx]
            next unless value

            records << { date: Date.parse(dates[date_idx]), base: iso, quote: "PTE", rate: Float(value) }
          end
        end
        records
      end

      private

      def build_url
        url = URI(BASE_URL)
        url.query = URI.encode_www_form(
          "lang" => "EN",
          "series_ids" => SERIES.keys.join(","),
        )
        url.to_s
      end

      def fetch_page(url)
        response = Net::HTTP.get(URI(url))
        Oj.load(response, mode: :strict)
      end

      def filter(records, after: nil, upto: nil)
        records.select! { |r| r[:date] >= after } if after
        records.select! { |r| r[:date] <= upto } if upto
        records
      end
    end
  end
end
