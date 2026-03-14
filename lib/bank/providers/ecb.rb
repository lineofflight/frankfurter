# frozen_string_literal: true

require "date"
require "net/http"
require "ox"

require "bank/provider"

module Bank
  module Providers
    class ECB < Provider
      CURRENT_URL = URI("https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml")
      NINETY_DAYS_URL = URI("https://www.ecb.europa.eu/stats/eurofxref/eurofxref-hist-90d.xml")
      HISTORICAL_URL = URI("https://www.ecb.europa.eu/stats/eurofxref/eurofxref-hist.xml")

      SUPPORTED_CURRENCIES = %w[
        AUD BGN BRL CAD CHF CNY CZK DKK GBP HKD HUF IDR ILS INR ISK JPY KRW MXN
        MYR NOK NZD PHP PLN RON SEK SGD THB TRY USD ZAR
      ].freeze

      def current
        parse(Net::HTTP.get(CURRENT_URL))
      end

      def ninety_days
        parse(Net::HTTP.get(NINETY_DAYS_URL))
      end

      def historical
        parse(Net::HTTP.get(HISTORICAL_URL))
      end

      def saved_data
        parse(File.read(File.join(__dir__, "..", "eurofxref-hist.xml")))
      end

      def supported_currencies
        SUPPORTED_CURRENCIES
      end

      private

      def parse(xml)
        Feed.new(xml)
      end

      class Feed
        include Enumerable

        def initialize(xml)
          @document = Ox.load(xml)
        end

        def each
          @document.locate("gesmes:Envelope/Cube/Cube").each do |day|
            yield({
              date: Date.parse(day["time"]),
              rates: day.nodes.each_with_object({}) do |currency, rates|
                rates[currency[:currency]] = Float(currency[:rate])
              end,
            })
          end
        end
      end
    end
  end
end
