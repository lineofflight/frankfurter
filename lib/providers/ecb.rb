# frozen_string_literal: true

require "csv"
require "net/http"

require "providers/base"

module Providers
  class ECB < Base
    SDMX_URL = "https://data-api.ecb.europa.eu/service/data/EXR/D..EUR.SP00.A"

    class << self
      def key = "ECB"
      def name = "European Central Bank"
      def base = "EUR"
    end

    def fetch(since: nil)
      url = URI(SDMX_URL)
      params = { format: "csvdata" }
      params[:startPeriod] = since.to_s if since
      url.query = URI.encode_www_form(params)

      @dataset = parse(Net::HTTP.get(url))
      self
    end

    def parse(csv)
      CSV.parse(csv, headers: true, liberal_parsing: true).filter_map do |row|
        next unless row["FREQ"] == "D"

        quote = row["CURRENCY"]
        next unless quote&.match?(/\A[A-Z]{3}\z/)

        rate = Float(row["OBS_VALUE"])
        date = Date.parse(row["TIME_PERIOD"])

        { provider: key, date:, base:, quote:, rate: }
      rescue ArgumentError, TypeError
        nil
      end
    end
  end
end
