# frozen_string_literal: true

require "json"
require "net/http"

require "providers/base"

module Providers
  # Bank of Canada daily indicative rates. Current series starts 2017-01-03.
  # Legacy noon rates (2007-2017) are available under the LEGACY_NOON_RATES group with ~65 currencies.
  class BOC < Base
    BASE_URL = "https://www.bankofcanada.ca/valet/observations/group/FX_RATES_DAILY/json"
    EARLIEST_DATE = "2017-01-03"

    def key = "BOC"
    def name = "Bank of Canada"
    def base = "CAD"

    def current
      records = fetch(recent: 1)
      last_date = records.last&.dig(:date)
      @dataset = records.select { |r| r[:date] == last_date }
      self
    end

    def historical(start_date: EARLIEST_DATE)
      @dataset = fetch(start_date:)
      self
    end

    private

    def fetch(**params)
      url = URI(BASE_URL)
      url.query = URI.encode_www_form(params)
      response = JSON.parse(Net::HTTP.get(url))

      response["observations"].flat_map do |obs|
        date = Date.parse(obs["d"])
        extract_rates(obs).map do |quote, rate|
          { provider: key, date:, base:, quote:, rate: }
        end
      end
    end

    def extract_rates(observation)
      observation.each_with_object({}) do |(series, data), rates|
        next unless series.start_with?("FX") && data.is_a?(Hash)

        iso = series.delete_prefix("FX").delete_suffix("CAD")
        rates[iso] = Float(data["v"])
      end
    end
  end
end
