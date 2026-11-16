# frozen_string_literal: true

require "json"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Central Bank of the Republic of Turkey. Uses the EVDS3 bulk API for indicative buying (A) and selling (S) rates in
    # TRY, in two series: forex (Döviz) as TCMB.FOREX and banknote (Efektif) as TCMB.BANKNOTE. Each rate is the mid of
    # buying and selling. Requires TCMB_API_KEY environment variable.
    class TCMB < Adapter
      EVDS_URL = "https://evds3.tcmb.gov.tr/igmevdsms-dis"
      # Hardcoded because the EVDS3 catalog API doesn't expose a clean list and the series rarely change. Browse:
      # https://evds3.tcmb.gov.tr > Exchange Rates > Indicative Exchange Rates
      CURRENCIES = [
        "AED",
        "AUD",
        "AZN",
        "CAD",
        "CHF",
        "CNY",
        "DKK",
        "EUR",
        "GBP",
        "JPY",
        "KRW",
        "KWD",
        "KZT",
        "NOK",
        "PKR",
        "QAR",
        "RON",
        "RUB",
        "SAR",
        "SEK",
        "USD",
      ].freeze

      # Series code infix per dataset: TP.DK.USD.A.YTL is the forex buying rate, TP.DK.USD.A.EF.YTL the banknote one.
      DATASETS = { "TCMB.FOREX" => "", "TCMB.BANKNOTE" => ".EF" }.freeze

      # JPY is quoted per 100 units in both series (confirmed via series metadata).
      UNITS = { "JPY" => 100 }.freeze

      SIDES = { buy: "A", sell: "S" }.freeze

      SERIES = DATASETS.flat_map do |dataset, infix|
        CURRENCIES.flat_map do |currency|
          SIDES.map { |side, code| ["TP.DK.#{currency}.#{code}#{infix}.YTL", [dataset, currency, side]] }
        end
      end.to_h.freeze

      class << self
        def api_key = ENV["TCMB_API_KEY"] || raise("no API key")
        def backfill_range = 730
      end

      def fetch(after: nil, upto: nil)
        start_date = after || Date.new(2012, 1, 2)
        end_date = upto || Date.today

        url = "#{EVDS_URL}/series=#{SERIES.keys.join("-")}" \
              "&startDate=#{start_date.strftime("%d-%m-%Y")}" \
              "&endDate=#{end_date.strftime("%d-%m-%Y")}" \
              "&type=json&frequency=1"

        parse(http.headers("key" => self.class.api_key).get(url).to_s)
      end

      def parse(json)
        JSON.parse(json).fetch("items", []).flat_map do |item|
          date = Date.strptime(item["Tarih"], "%d-%m-%Y")
          quotes = {}
          SERIES.each do |series, (dataset, currency, side)|
            value = item[series.tr(".", "_")]
            (quotes[[dataset, currency]] ||= {})[side] = Float(value) if value
          end

          # The API returns zero for a currency without a rate in the series, as most have none in banknotes.
          quotes.filter_map do |(dataset, currency), quote|
            buy = quote[:buy]
            sell = quote[:sell]
            next unless buy&.positive? && sell&.positive?

            unit = UNITS.fetch(currency, 1)
            { dataset:, date:, base: currency, quote: "TRY", rate: midpoint(buy, sell) / unit,
              **prices(bid: buy, ask: sell, unit:), }
          end
        end
      end
    end
  end
end
