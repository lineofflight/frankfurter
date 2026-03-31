# frozen_string_literal: true

require "json"
require "net/http"

require "providers/base"

module Providers
  # Banco de la República Colombia. Publishes the daily TRM (Tasa
  # Representativa del Mercado) — the representative market exchange rate
  # for the US dollar against the Colombian peso — via the Socrata Open
  # Data API on datos.gov.co.
  class BANREP < Base
    BASE_URL = "https://www.datos.gov.co/resource/32sa-8pi3.json"
    EARLIEST_DATE = Date.new(2000, 1, 3)

    class << self
      def key = "BANREP"
      def name = "Banco de la República"
      def earliest_date = EARLIEST_DATE
    end

    def fetch(since: nil, upto: nil)
      start_date = since || EARLIEST_DATE
      end_date = upto || Date.today

      uri = URI(BASE_URL)
      uri.query = URI.encode_www_form(
        "$where" => "vigenciadesde>='#{start_date}T00:00:00.000' AND vigenciadesde<='#{end_date}T00:00:00.000'",
        "$limit" => 50_000,
        "$order" => "vigenciadesde ASC",
      )

      response = Net::HTTP.start(uri.host, uri.port, use_ssl: true, open_timeout: 15, read_timeout: 30) do |http|
        http.request(Net::HTTP::Get.new(uri))
      end

      @dataset = parse(response.body)
      self
    rescue Net::OpenTimeout, Net::ReadTimeout, Socket::ResolutionError, JSON::ParserError
      @dataset = []
      self
    end

    def parse(json)
      records = json.is_a?(String) ? JSON.parse(json) : json
      return [] unless records.is_a?(Array)

      records.filter_map do |record|
        date_str = record["vigenciadesde"]
        value_str = record["valor"]
        next if date_str.nil? || value_str.nil?

        date = Date.parse(date_str)
        rate_value = Float(value_str)
        next if rate_value.zero?

        { provider: key, date:, base: "USD", quote: "COP", rate: rate_value }
      rescue ArgumentError, TypeError
        nil
      end
    end
  end
end
