# frozen_string_literal: true

require "date"
require "ox"
require "stringio"
require "zip"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Reserve Bank of Fiji. Publishes the daily mid-rate series "8.8 Exchange
    # Rates Daily" as a single rolling XLSX (~580 KB, ~6,400 daily rows) covering
    # 2001-01-02 to present. Eight quote currencies: SDR, STG (GBP), YEN (JPY),
    # CHF, EURO (EUR), A$ (AUD), NZ$ (NZD), US$ (USD).
    #
    # The XLSX URL embeds the publication year/month under /wp-content/uploads/
    # YYYY/MM/, so the adapter scrapes the economic-and-financial-statistics hub
    # for the current link rather than hardcoding a path.
    #
    # Header reads "RBF Mid-Rate Per Fiji Dollar", so each row records "1 FJD =
    # X foreign". Records are emitted with FJD as base and the foreign currency
    # as quote, matching the pivot-in-base convention used by ECB. SDR is
    # relabelled to its ISO 4217 code XDR on emit.
    #
    # USD is set at 09:00 Fiji time (UTC+12) each business day; cross rates are
    # derived from that fix. No explicit terms-of-use page; the site footer
    # carries a liability disclaimer only.
    class RBF < Adapter
      HUB_URL = "https://www.rbf.gov.fj/statistics/economic-and-financial-statistics/"
      ARCHIVE_LINK = %r{href="(https://www\.rbf\.gov\.fj/wp-content/uploads/\d{4}/\d{2}/8\.8-Exchange-Rates-Daily[^"]*\.xlsx)"}
      EXCEL_EPOCH = Date.new(1899, 12, 30)

      # Maps the workbook's non-ISO column labels to ISO 4217 codes.
      CURRENCIES = {
        "SDR" => "XDR",
        "STG" => "GBP",
        "YEN" => "JPY",
        "CHF" => "CHF",
        "EURO" => "EUR",
        "A$" => "AUD",
        "NZ$" => "NZD",
        "US$" => "USD",
      }.freeze

      class << self
        # The full series ships as a single workbook refreshed daily, so a
        # large range keeps the fetch in one download.
        def backfill_range = 36_525
      end

      def fetch(after: nil, upto: nil)
        url = locate_archive_url
        parse(download(url), after: after, upto: upto)
      end

      def parse(xlsx_bytes, after: nil, upto: nil)
        records = []

        Zip::File.open_buffer(StringIO.new(xlsx_bytes)) do |zip|
          strings = shared_strings(zip)
          sheet_xml = zip.find_entry("xl/worksheets/sheet1.xml").get_input_stream.read
          doc = Ox.parse(sheet_xml)

          column_map = {}
          doc.locate("*/sheetData/row").each do |row|
            if column_map.empty?
              column_map = extract_column_map(row, strings)
              next
            end

            date = nil
            row_rates = {}

            row.locate("c").each do |cell|
              ref = cell["r"]
              next unless ref

              column = ref.match(/\A([A-Z]+)/)[1]
              next if cell["t"] == "s"

              value = cell.locate("v").first
              next unless value

              text = value.text
              next if text.nil? || text.empty?

              if column == "A"
                serial = Integer(text, exception: false) || Float(text, exception: false)&.to_i
                date = EXCEL_EPOCH + serial if serial
              else
                iso = column_map[column]
                next unless iso

                rate = Float(text, exception: false)
                next unless rate&.positive?

                row_rates[iso] = rate
              end
            end

            next unless date
            next if after && date < after
            next if upto && date > upto

            row_rates.each do |quote, rate|
              records << { date: date, base: "FJD", quote: quote, rate: rate }
            end
          end
        end

        records
      end

      private

      def locate_archive_url
        body = download(HUB_URL)
        match = body.match(ARCHIVE_LINK)
        raise "RBF: exchange-rates XLSX link not found on #{HUB_URL}" unless match

        match[1]
      end

      def download(url)
        http.get(url).to_s
      end

      def read_timeout = 120

      def shared_strings(zip)
        entry = zip.find_entry("xl/sharedStrings.xml")
        return [] unless entry

        Ox.parse(entry.get_input_stream.read).locate("sst/si").map do |si|
          si.locate("t").map(&:text).join
        end
      end

      # The header row carries shared-string labels for the eight currency
      # columns. We resolve each cell's string index to its label and map it
      # to an ISO code via CURRENCIES.
      def extract_column_map(row, strings)
        map = {}

        row.locate("c").each do |cell|
          next unless cell["t"] == "s"

          ref = cell["r"]
          next unless ref

          column = ref.match(/\A([A-Z]+)/)[1]
          value = cell.locate("v").first
          next unless value

          index = Integer(value.text, exception: false)
          next unless index

          label = strings[index].to_s.strip
          iso = CURRENCIES[label]
          map[column] = iso if iso
        end

        map
      end
    end
  end
end
