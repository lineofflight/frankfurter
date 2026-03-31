# frozen_string_literal: true

require "json"
require "net/http"

require "providers/base"

module Providers
  # Banco de México. Fetches daily FIX and reference exchange rates for 5
  # currencies against the Mexican peso (MXN) via the SIE REST API.
  # Supports batched series queries and date range filtering.
  class BANXICO < Base
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
      def key = "BANXICO"
      def name = "Banco de México"
      def api_key? = true
      def api_key = ENV["BANXICO_API_KEY"]
    end

    def fetch(since: nil, upto: nil)
      ids = SERIES.keys.join(",")
      url = if since
        end_date = upto || Date.today
        URI("#{BASE_URL}/#{ids}/datos/#{since}/#{end_date}")
      else
        URI("#{BASE_URL}/#{ids}/datos")
      end

      response = Net::HTTP.start(url.host, url.port, use_ssl: true) do |http|
        request = Net::HTTP::Get.new(url)
        request["Bmx-Token"] = api_key
        http.request(request)
      end

      @dataset = parse(response.body)
      self
    rescue Net::OpenTimeout, Net::ReadTimeout, Socket::ResolutionError, OpenSSL::SSL::SSLError
      @dataset = []
      self
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

          rate = Float(value)
          next if rate.zero?

          date = Date.strptime(obs["fecha"], "%d/%m/%Y")
          { provider: key, date:, base:, quote: "MXN", rate: }
        rescue ArgumentError, TypeError
          nil
        end
      end
    end
  end
end
