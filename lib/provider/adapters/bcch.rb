# frozen_string_literal: true

require "json"
require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banco Central de Chile. Fetches daily exchange rates for 8 currencies
    # against the Chilean peso (CLP) via the BDE REST API. Requires registered
    # credentials (email + password) passed as query parameters.
    class BCCH < Adapter
      API_URL = "https://si3.bcentral.cl/SieteRestWS/SieteRestWS.ashx"

      # series_id => base currency (all quoted against CLP)
      SERIES = {
        "F073.TCO.PRE.Z.D" => "USD",
        "F072.CLP.EUR.N.O.D" => "EUR",
        "F072.CLP.GBP.N.O.D" => "GBP",
        "F072.CLP.JPY.N.O.D" => "JPY",
        "F072.CLP.CAD.N.O.D" => "CAD",
        "F072.CLP.AUD.N.O.D" => "AUD",
        "F072.CLP.CNY.N.O.D" => "CNY",
        "F072.CLP.BRL.N.O.D" => "BRL",
      }.freeze

      class << self
        def api_key = ENV["BCCH_USER"] || raise(Adapter::ApiKeyMissing)
      end

      def fetch(after: nil, upto: nil)
        dataset = []
        params = {}
        params[:firstdate] = after.to_s if after
        params[:lastdate] = (upto || Date.today).to_s if after

        SERIES.each do |series_id, base|
          sleep(0.2)
          dataset.concat(fetch_series(series_id, base, **params))
        end

        dataset
      end

      def parse(json, base)
        data = JSON.parse(json)
        observations = data.dig("Series", "Obs") || []

        observations.filter_map do |obs|
          next if obs["statusCode"] != "OK"

          value = obs["value"]&.tr(",", "")
          next unless value

          rate = Float(value)
          next if rate.zero?

          date = Date.strptime(obs["indexDateString"], "%d-%m-%Y")
          { date:, base:, quote: "CLP", rate: }
        end
      end

      private

      def fetch_series(series_id, base, **params)
        url = URI(API_URL)
        url.query = URI.encode_www_form(
          user: ENV["BCCH_USER"],
          pass: ENV["BCCH_PASS"],
          function: "GetSeries",
          timeseries: series_id,
          **params,
        )

        response = Net::HTTP.get(url)
        parse(response, base)
      end
    end
  end
end
