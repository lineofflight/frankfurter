# frozen_string_literal: true

require "net/http"
require "ox"

module Bank
  class Feed
    include Enumerable

    class << self
      def current
        url = URI("https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml")
        xml = Net::HTTP.get(url)

        new(xml)
      end

      def ninety_days
        url = URI("https://www.ecb.europa.eu/stats/eurofxref/eurofxref-hist-90d.xml")
        xml = Net::HTTP.get(url)

        new(xml)
      end

      def historical
        url = URI("https://www.ecb.europa.eu/stats/eurofxref/eurofxref-hist.xml")
        xml = Net::HTTP.get(url)

        new(xml)
      end

      def saved_data
        xml = File.read(File.join(__dir__, "eurofxref-hist.xml"))
        new(xml)
      end
    end

    def initialize(xml)
      @document = Ox.load(xml)
    end

    def each
      @document.locate("gesmes:Envelope/Cube/Cube").each do |day|
        yield(date: Date.parse(day["time"]),
              rates: day.nodes.to_h do |currency|
                       [currency[:currency], Float(currency[:rate])]
                     end)
      end
    end
  end
end
