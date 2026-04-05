# frozen_string_literal: true

require "net/http"
require "ox"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Seðlabanki Íslands (Central Bank of Iceland). Publishes daily reference
    # exchange rates for 30+ currencies against the Icelandic króna (ISK).
    # Two GroupIDs are needed: 9 (official reference, 10 currencies from 1981)
    # and 7 (registered mid-rate, 22 currencies from 2006).
    class SBI < Adapter
      BASE_URL = "https://sedlabanki.is/xmltimeseries/Default.aspx"
      # TimeSeries ID to ISO currency code mapping
      # GroupID=9 (official reference rates)
      GROUP9_CURRENCIES = {
        "4055" => "USD",
        "4061" => "DKK",
        "4064" => "EUR",
        "4085" => "JPY",
        "4088" => "CAD",
        "4091" => "NOK",
        "4097" => "XDR",
        "4103" => "GBP",
        "4106" => "CHF",
        "4109" => "SEK",
      }.freeze

      # GroupID=7 (registered mid-rates)
      GROUP7_CURRENCIES = {
        "29" => "CNY",
        "31" => "PLN",
        "35" => "NGN",
        "36" => "TWD",
        "37" => "KRW",
        "38" => "SRD",
        "39" => "AUD",
        "40" => "NZD",
        "41" => "HKD",
        "42" => "HUF",
        "43" => "ILS",
        "44" => "ZAR",
        "45" => "SGD",
        "46" => "MXN",
        "48" => "TRY",
        "50" => "INR",
        "52" => "CZK",
        "53" => "BRL",
        "74" => "THB",
        "288" => "JMD",
        "3503" => "SAR",
        "19254" => "KWD",
      }.freeze

      class << self
        def backfill_range = 365
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today

        xml9 = fetch_group(after, end_date, 9)
        xml7 = fetch_group(after, end_date, 7)

        @dataset = parse(xml9, GROUP9_CURRENCIES) + parse(xml7, GROUP7_CURRENCIES)
      end

      def parse(xml, currency_map = GROUP9_CURRENCIES.merge(GROUP7_CURRENCIES))
        doc = Ox.load(xml, mode: :generic)
        records = []

        doc.locate("*/TimeSeries").each do |ts|
          id = ts.attributes[:ID]
          currency = currency_map[id]
          next unless currency

          ts.locate("TimeSeriesData/Entry").each do |entry|
            date_node = entry.locate("Date").first
            value_node = entry.locate("Value").first
            next unless date_node && value_node

            date_str = date_node.text
            rate_str = value_node.text
            next unless date_str && rate_str

            rate = Float(rate_str)
            next if rate.zero?

            date = parse_date(date_str)
            records << { date:, base: currency, quote: "ISK", rate: }
          end
        end

        records
      end

      private

      def fetch_group(start_date, end_date, group_id)
        url = URI(BASE_URL)
        url.query = URI.encode_www_form(
          "DagsFra" => start_date.to_s,
          "DagsTil" => end_date.to_s,
          "GroupID" => group_id,
          "Type" => "xml",
        )

        Net::HTTP.get(url)
      end

      def parse_date(str)
        # Format: "M/D/YYYY 12:00:00 AM"
        date_part = str.split(" ").first
        month, day, year = date_part.split("/").map(&:to_i)
        Date.new(year, month, day)
      end
    end
  end
end
