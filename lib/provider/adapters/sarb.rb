# frozen_string_literal: true

require "net/http"
require "oj"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # South African Reserve Bank. Publishes daily weighted-average exchange rates
    # for 24 currencies. USD, GBP, EUR are quoted as ZAR per foreign unit (foreign
    # base); all others as foreign per ZAR (ZAR base).
    class SARB < Adapter
      BASE_URL = "https://custom.resbank.co.za/SarbWebApi/WebIndicators/Shared/GetTimeseriesObservations"

      SERIES = {
        "EXCX135D" => ["USD", "ZAR"],
        "EXCZ001D" => ["GBP", "ZAR"],
        "EXCZ002D" => ["EUR", "ZAR"],
        "EXCB080D" => ["ZAR", "AUD"],
        "EXCB051D" => ["ZAR", "BWP"],
        "EXCB036D" => ["ZAR", "BRL"],
        "EXCB031D" => ["ZAR", "CAD"],
        "EXCB121D" => ["ZAR", "CNY"],
        "EXCB003D" => ["ZAR", "DKK"],
        "EXCB122D" => ["ZAR", "HKD"],
        "EXCB123D" => ["ZAR", "INR"],
        "EXCB094D" => ["ZAR", "ILS"],
        "EXCB120D" => ["ZAR", "JPY"],
        "EXCB059D" => ["ZAR", "KES"],
        "EXCB063D" => ["ZAR", "MWK"],
        "EXCB081D" => ["ZAR", "NZD"],
        "EXCB013D" => ["ZAR", "NOK"],
        "EXCB015D" => ["ZAR", "SEK"],
        "EXCB016D" => ["ZAR", "CHF"],
        "EXCB126D" => ["ZAR", "TWD"],
        "EXCB115D" => ["ZAR", "THB"],
        "EXCB071D" => ["ZAR", "ZMW"],
        "EXCB118D" => ["ZAR", "KRW"],
      }.freeze

      class << self
        def backfill_range = 365
      end

      def fetch(after: nil, upto: nil)
        @dataset = []
        start_date = after.to_s
        end_date = (upto || Date.today).to_s

        SERIES.each do |code, (base, quote)|
          sleep(0.2)
          @dataset.concat(fetch_series(code, start_date, end_date, base:, quote:))
        end

        @dataset
      end

      def parse(json, base:, quote:)
        observations = Oj.load(json, mode: :strict)

        observations.filter_map do |obs|
          value = obs["Value"]
          next if value.nil? || value.to_s.strip.empty?

          rate = Float(value)
          next if rate.zero?

          date = Date.parse(obs["Period"])
          { date:, base:, quote:, rate: }
        end
      end

      private

      def fetch_series(code, start_date, end_date, base:, quote:)
        url = URI("#{BASE_URL}/#{code}/#{start_date}/#{end_date}")
        response = Net::HTTP.get(url)
        parse(response, base:, quote:)
      end
    end
  end
end
