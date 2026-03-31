# frozen_string_literal: true

require "json"
require "net/http"

require "providers/base"

module Providers
  # Banco Central do Brasil. Fetches daily PTAX closing exchange rates for 10
  # currencies against the Brazilian real (BRL) via the PTAX OData API.
  class BCB < Base
    API_URL = "https://olinda.bcb.gov.br/olinda/servico/PTAX/versao/v1/odata/" \
      "CotacaoMoedaPeriodo(moeda=@moeda,dataInicial=@dataInicial,dataFinalCotacao=@dataFinalCotacao)"

    CURRENCIES = ["AUD", "CAD", "CHF", "DKK", "EUR", "GBP", "JPY", "NOK", "SEK", "USD"].freeze

    class << self
      def key = "BCB"
      def name = "Banco Central do Brasil"
      def earliest_date = Date.new(2000, 1, 1)
    end

    def fetch(since: nil, upto: nil)
      @dataset = []
      date_from = since || self.class.earliest_date
      date_upto = upto || Date.today

      CURRENCIES.each do |currency|
        sleep(0.2)
        @dataset.concat(fetch_currency(currency, date_from, date_upto))
      end

      self
    rescue Net::OpenTimeout, Net::ReadTimeout, Socket::ResolutionError, OpenSSL::SSL::SSLError
      @dataset = []
      self
    end

    def parse(json, currency)
      data = JSON.parse(json)
      records = data["value"] || []

      records.filter_map do |record|
        rate = record["cotacaoVenda"]
        next unless rate
        next if rate.zero?

        raw_date = record["dataHoraCotacao"]
        next unless raw_date

        date = Date.strptime(raw_date, "%Y-%m-%d")
        { provider: key, date:, base: currency, quote: "BRL", rate: }
      rescue ArgumentError, TypeError
        nil
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
    rescue JSON::ParserError
      []
    end
  end
end
