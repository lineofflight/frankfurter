# frozen_string_literal: true

require "net/http"
require "ox"

require "providers/base"

module Providers
  class ECB < Base
    CURRENT_URL = URI("https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml")
    HISTORICAL_URL = URI("https://www.ecb.europa.eu/stats/eurofxref/eurofxref-hist.xml")

    def key = "ECB"
    def name = "European Central Bank"
    def base = "EUR"

    def current
      @dataset = parse(Net::HTTP.get(CURRENT_URL))
      self
    end

    def historical
      @dataset = parse(Net::HTTP.get(HISTORICAL_URL))
      self
    end

    def parse(xml)
      Ox.load(xml).locate("gesmes:Envelope/Cube/Cube").flat_map do |day|
        date = Date.parse(day["time"])
        day.nodes.map do |c|
          rate = Float(c[:rate])
          { provider: key, date:, base:, quote: c[:currency], rate: }
        end
      end
    end
  end
end
