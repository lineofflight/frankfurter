# frozen_string_literal: true

require "json"
require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banco Central do Brasil. Fetches daily PTAX closing exchange rates for 10
    # currencies against the Brazilian real (BRL) via the PTAX OData API.
    # The PTAX system only publishes these 10 currencies.
    class BCB < Adapter
      API_URL = "https://olinda.bcb.gov.br/olinda/servico/PTAX/versao/v1/odata/" \
        "CotacaoMoedaPeriodo(moeda=@moeda,dataInicial=@dataInicial,dataFinalCotacao=@dataFinalCotacao)"

      CURRENCIES = ["AUD", "CAD", "CHF", "DKK", "EUR", "GBP", "JPY", "NOK", "SEK", "USD"].freeze

      def fetch(after: nil, upto: nil)
        dataset = []
        date_from = Date.parse(after.to_s)
        date_upto = Date.parse((upto || Date.today).to_s)

        CURRENCIES.each do |currency|
          sleep(0.2)
          dataset.concat(fetch_currency(currency, date_from, date_upto))
        end

        dataset
      end

      def parse(json, currency)
        data = JSON.parse(json)
        records = data["value"] || []

        records.filter_map do |record|
          rate = record["cotacaoVenda"]
          next unless rate
          next if rate.zero?

          raw_date = record["dataHoraCotacao"].to_s.strip[0, 10]
          next unless raw_date&.length == 10

          date = Date.strptime(raw_date, "%Y-%m-%d")
          { date:, base: currency, quote: "BRL", rate: }
        end
      end

      private

      def fetch_currency(currency, date_from, date_upto)
        query = [
          "@moeda='#{currency}'",
          "@dataInicial='#{date_from.strftime("%m-%d-%Y")}'",
          "@dataFinalCotacao='#{date_upto.strftime("%m-%d-%Y")}'",
          "$filter=tipoBoletim%20eq%20'Fechamento'",
          "$format=json",
        ].join("&")

        url = URI("#{API_URL}?#{query}")
        response = Net::HTTP.get(url)
        parse(response, currency)
      end
    end
  end
end
