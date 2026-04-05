# frozen_string_literal: true

require "json"
require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banco de México. Fetches daily FIX and reference exchange rates for 5
    # currencies against the Mexican peso (MXN) via the SIE REST API.
    # Supports batched series queries and date range filtering.
    class BANXICO < Adapter
      BASE_URL = "https://www.banxico.org.mx/SieAPIRest/service/v1/series"

      # series_id => base currency (all quoted against MXN)
      SERIES = {
        "SF43718" => "USD",
        "SF46410" => "EUR",
        "SF46407" => "GBP",
        "SF46406" => "JPY",
        "SF60632" => "CAD",
      }.freeze

      class << self
        def api_key = ENV["BANXICO_API_KEY"] || raise(Adapter::ApiKeyMissing)
      end

      def fetch(after: nil, upto: nil)
        ids = SERIES.keys.join(",")
        url = if after
          end_date = upto || Date.today
          URI("#{BASE_URL}/#{ids}/datos/#{after}/#{end_date}")
        else
          URI("#{BASE_URL}/#{ids}/datos")
        end

        response = Net::HTTP.start(url.host, url.port, use_ssl: true) do |http|
          request = Net::HTTP::Get.new(url)
          request["Bmx-Token"] = self.class.api_key
          http.request(request)
        end

        parse(response.body)
      end

      def parse(json)
        data = JSON.parse(json)
        series = data.dig("bmx", "series") || []

        series.flat_map do |s|
          base = SERIES[s["idSerie"]]
          next [] unless base

          (s["datos"] || []).filter_map do |obs|
            value = obs["dato"]&.tr(",", "")
            next unless value

            # Skip "N/E" (not available) values
            rate = Float(value, exception: false)
            next unless rate&.positive?

            date = Date.strptime(obs["fecha"], "%d/%m/%Y")
            { date:, base:, quote: "MXN", rate: }
          end
        end
      end
    end
  end
end
