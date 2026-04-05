# frozen_string_literal: true

require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Central Bank of West African States (Banque Centrale des Etats de l'Afrique de l'Ouest).
    # Fetches daily reference exchange rates for 27 currencies against the CFA Franc (XOF).
    # The API only accepts a single date per request, so we iterate day by day skipping weekends.
    class BCEAO < Adapter
      BASE_URL = "https://www.bceao.int/fr/cours/get_all_reference_by_date"
      CURRENCY_MAP = {
        "Euro" => "EUR",
        "Dollar us" => "USD",
        "Yen japonais" => "JPY",
        "Couronne danoise" => "DKK",
        "Couronne suédoise" => "SEK",
        "Livre sterling" => "GBP",
        "Couronne norvégienne" => "NOK",
        "Couronne thèque" => "CZK",
        "Forint hongrois" => "HUF",
        "Zloty polonais" => "PLN",
        "Franc suisse" => "CHF",
        "Dollar canadien" => "CAD",
        "Dollar australien" => "AUD",
        "Dollar néo-zélandais" => "NZD",
        "Rand sud-africain" => "ZAR",
        "Yuan chinois" => "CNY",
        "Roupie Indienne" => "INR",
        "Baht thailandais" => "THB",
        "Real brésilien" => "BRL",
        "Dollar singapourien" => "SGD",
        "Nouvelle livre turque" => "TRY",
        "Dirham Emirats Arabes Unis" => "AED",
        "Nouveau Shekel" => "ILS",
        "Won Coréen" => "KRW",
        "Dollar Hong Kong" => "HKD",
        "Ryal Saudien" => "SAR",
        "Dinar Koweitien" => "KWD",
      }.freeze

      class << self
        def backfill_range = 90
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        dataset = []

        first = true
        after.upto(end_date) do |date|
          # Publishes Sun-Fri; Sunday covers Gulf markets (AED, SAR, KWD)
          next if date.saturday?

          sleep(1) unless first
          first = false

          dataset.concat(fetch_date(date))
        end

        dataset
      end

      def parse(html, date:)
        return [] unless html.include?("<table")

        records = []
        html.scan(%r{<tr>[^<]*<td>([^<]*)</td>[^<]*<td>([\d.,]+)</td>[^<]*</tr>}) do |name, rate_str|
          name = name.strip
          iso = CURRENCY_MAP[name]
          next unless iso

          # Rates normally use French format (period=thousands, comma=decimal).
          # Fall back to English format if no comma is present.
          rate_value = if rate_str.include?(",")
            Float(rate_str.delete(".").tr(",", "."))
          else
            Float(rate_str)
          end
          next if rate_value.zero?

          records << { date:, base: iso, quote: "XOF", rate: rate_value }
        end

        records
      end

      private

      def fetch_date(date)
        url = URI("#{BASE_URL}?dateJour=#{date.strftime("%Y-%m-%d")}")
        response = Net::HTTP.get(url)
        parse(response, date:)
      end
    end
  end
end
