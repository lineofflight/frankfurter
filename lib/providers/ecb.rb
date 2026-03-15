# frozen_string_literal: true

require "net/http"
require "ox"

require "providers/base"

module Providers
  class ECB < Base
    CURRENT_URL = URI("https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml")
    HISTORICAL_URL = URI("https://www.ecb.europa.eu/stats/eurofxref/eurofxref-hist.xml")

    def key = "ECB"
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
      Ox.load(xml).locate("gesmes:Envelope/Cube/Cube").map do |day|
        {
          date: Date.parse(day["time"]),
          rates: day.nodes.to_h { |c| [c[:currency], Float(c[:rate])] },
        }
      end
    end
  end
end
