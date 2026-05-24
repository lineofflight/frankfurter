# frozen_string_literal: true

require "net/http"
require "ox"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Central Bank of Turkmenistan. Publishes daily official rates for ~45 currencies
    # against TMT (Turkmenistani manat). XML archive goes back to 2020-04-17 at
    # https://cbt.tm/kurs/YYYY/DDMMYYYY.xml. Sundays return 404; non-trading days are
    # skipped silently.
    #
    # USD has been administratively pegged at 3.5 TMT since 2015-01. The non-USD basket
    # (EUR/RUB/CNY and regional CIS currencies) is derived from CBT's USD cross via
    # rate_tmt, which is the value we use directly.
    #
    # Each <rate> entry carries:
    #   - rate_usd: foreign-per-USD cross used internally by CBT (not used here)
    #   - multiplier: nominal unit count (1, 10, 100, 1000, 10000)
    #   - rate_tmt: TMT per <multiplier> units of the foreign currency
    #
    # The published rate_tmt is per-multiplier; divide by multiplier to get per-unit.
    # The <date> element appears as either DD.MM.YYYY (2024+) or YYYY-MM-DD (2020-2023).
    #
    # The endpoint geo-blocks default curl User-Agents (returns 403), so we send a
    # browser UA. The site is occasionally unreachable from outside Turkmenistan;
    # transient SocketError/Timeout are retried by the base class.
    #
    # Direction: provider publishes "N foreign = X TMT", so foreign currency goes in
    # base and TMT in quote, matching the pivot-in-quote convention used by NBG/NBT/BBK.
    class CBT < Adapter
      USER_AGENT = "Mozilla/5.0 (compatible; Frankfurter/2.0; +https://frankfurter.dev)"

      class << self
        def backfill_range = 1
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        dataset = []

        (after..end_date).each do |date|
          next if date.sunday?

          dataset.concat(fetch_date(date))
        end

        dataset
      end

      def parse(xml, expected_date: nil)
        doc = Ox.load(xml)
        root = doc.locate("cbt_currency_rate").first
        return [] unless root

        date_str = root.locate("date/^String").first
        return [] unless date_str

        date = parse_date(date_str)
        return [] unless date
        return [] if expected_date && date != expected_date

        root.locate("rate").filter_map do |node|
          code = node[:code]
          next unless code&.match?(/\A[A-Z]{3}\z/)

          multiplier_str = node.locate("multiplier/^String").first
          rate_str = node.locate("rate_tmt/^String").first
          next unless multiplier_str && rate_str

          multiplier = Integer(multiplier_str, exception: false)
          rate = Float(rate_str, exception: false)
          next unless multiplier && rate
          next if multiplier.zero? || rate.zero?

          { date:, base: code, quote: "TMT", rate: rate / multiplier }
        end
      end

      private

      def parse_date(str)
        if str.match?(/\A\d{2}\.\d{2}\.\d{4}\z/)
          Date.strptime(str, "%d.%m.%Y")
        elsif str.match?(/\A\d{4}-\d{2}-\d{2}\z/)
          Date.strptime(str, "%Y-%m-%d")
        end
      end

      def fetch_date(date)
        sleep(0.2)
        uri = URI("https://cbt.tm/kurs/#{date.year}/#{date.strftime("%d%m%Y")}.xml")
        request = Net::HTTP::Get.new(uri)
        request["User-Agent"] = USER_AGENT

        response = Net::HTTP.start(uri.host, uri.port, use_ssl: true, open_timeout: 15, read_timeout: 30) do |http|
          http.request(request)
        end

        return [] if response.code == "404"

        response.value
        parse(response.body, expected_date: date)
      end
    end
  end
end
