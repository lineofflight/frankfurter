# frozen_string_literal: true

require "net/http"
require "ox"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # National Bank of the Kyrgyz Republic. Publishes daily rates for 5 major currencies
    # (USD, EUR, RUB, KZT, CNY) and weekly rates for ~35 others against KGS.
    #
    # Two XML endpoints are merged in a single fetch: daily.xml for the majors and
    # weekly.xml for the rest. Each weekly record carries a 7-day ValidFor window;
    # we stamp it with the publish date and let CarryForward (14-day lookback) fill
    # the gaps between weekly publications.
    #
    # XML is Windows-1251 encoded with comma decimal separators. Values are normalized
    # by Nominal (some weekly currencies are quoted per 10 or 100 units).
    #
    # Records are returned in NBKR's native direction: foreign currency as base, KGS
    # as quote (1 unit foreign = X KGS), matching the convention used by other
    # pivot-in-quote adapters (NBG, CBR, BBK).
    #
    # Historical data (1999-present via HTML scrape, 1993-2009 via XLS archive) is
    # intentionally not implemented here; this adapter only emits rates published
    # from now forward via the XML endpoints.
    class NBKR < Adapter
      DAILY_URL = URI("https://www.nbkr.kg/XML/daily.xml")
      WEEKLY_URL = URI("https://www.nbkr.kg/XML/weekly.xml")

      def fetch(after: nil, upto: nil)
        records = parse(Net::HTTP.get(DAILY_URL)) + parse(Net::HTTP.get(WEEKLY_URL))

        records.select! { |r| r[:date] >= after } if after
        records.select! { |r| r[:date] <= upto } if upto
        records
      end

      def parse(xml)
        xml = xml.dup.force_encoding(Encoding::WINDOWS_1251).encode(Encoding::UTF_8)
        root = Ox.load(xml).locate("CurrencyRates").first
        return [] unless root

        date_attr = root[:Date]
        return [] unless date_attr

        date = Date.strptime(date_attr, "%d.%m.%Y")

        root.locate("Currency").filter_map do |node|
          code = node[:ISOCode]
          next unless code&.match?(/\A[A-Z]{3}\z/)

          nominal = node.locate("Nominal").first&.text.to_i
          next if nominal.zero?

          value = node.locate("Value").first&.text
          next if value.nil? || value.empty?

          rate = Float(value.tr(",", "."), exception: false)
          next unless rate&.positive?

          { date:, base: code, quote: "KGS", rate: rate / nominal }
        end
      end
    end
  end
end
