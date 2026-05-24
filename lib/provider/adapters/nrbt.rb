# frozen_string_literal: true

require "date"
require "net/http"
require "openssl"
require "ox"
require "stringio"
require "uri"
require "zip"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # National Reserve Bank of Tonga — "Authorized Persons' Average Daily Exchange Rates"
    # published as a single rolling XLSX covering 2017-01 to present in five biennial
    # sheets ("2017 to 2018", "2019 to 2020", ..., "2025 to 2026"). Each sheet has
    # the same column layout: one date column (Excel serial in column A) followed by
    # three blocks of twelve quote currencies — BUY (B-M), MID (O-Z), and SELL
    # (AB-AM), with column N and AA as visual spacers. Quote currencies, in the order
    # they appear in each block: AUD, EUR, FJD, GBP, JPY, NZD, USD, WST, CHF, CAD,
    # SEK, SGD.
    #
    # Rates are published as "1 TOP = X foreign" — TOP is the base, foreign is the
    # quote. This matches the ECB convention (pivot in base). We emit MID directly
    # per the issue #314 pattern of preferring a published mid over reconstructing
    # it from buy/sell.
    #
    # Holidays and bank closures are flagged inline as shared strings in the rate
    # cells (e.g. "Public Holiday: ANZAC Day"); those rows have no numeric values
    # and are skipped.
    class NRBT < Adapter
      DATA_URL = "https://www.reservebank.to/data/docs/fmarkets/exrates/average_daily_exchange_rates.xlsx"
      EXCEL_EPOCH = Date.new(1899, 12, 30)

      # Twelve quote currencies in column order. The same sequence is repeated three
      # times across the BUY / MID / SELL blocks.
      QUOTE_CURRENCIES = ["AUD", "EUR", "FJD", "GBP", "JPY", "NZD", "USD", "WST", "CHF", "CAD", "SEK", "SGD"].freeze

      # Column indexes (zero-based) of each MID rate. Columns A=0, B=1 ... M=12 hold
      # the date + BUY block; N=13 is a spacer; O=14 ... Z=25 hold MID; AA=26 is a
      # spacer; AB=27 ... AM=38 hold SELL.
      MID_COLUMN_RANGE = 14..25

      class << self
        # Whole-archive fetch — a single XLSX holds the full history. Large range
        # keeps it in one fetch.
        def backfill_range = 36_525
      end

      def fetch(after: nil, upto: nil)
        xlsx_data = download(URI(DATA_URL))
        parse(xlsx_data, after: after, upto: upto)
      end

      def parse(xlsx_data, after: nil, upto: nil)
        dataset = []

        Zip::File.open_buffer(StringIO.new(xlsx_data)) do |zip|
          workbook = Ox.parse(zip.read("xl/workbook.xml"))
          rels = parse_rels(zip.read("xl/_rels/workbook.xml.rels"))

          workbook.locate("*/sheets/sheet").each do |sheet_node|
            rid = sheet_node["r:id"] || sheet_node["id"]
            target = rels[rid]
            next unless target

            sheet_xml = zip.read("xl/#{target}")
            dataset.concat(parse_sheet(sheet_xml, after: after, upto: upto))
          end
        end

        dataset
      end

      private

      def download(uri)
        http = Net::HTTP.new(uri.host, uri.port)
        http.use_ssl = (uri.scheme == "https")
        http.open_timeout = 30
        http.read_timeout = 120

        response = http.get(uri.request_uri)
        response.value
        response.body
      end

      def parse_rels(xml)
        Ox.parse(xml).locate("*/Relationship").to_h do |rel|
          [rel["Id"], rel["Target"]]
        end
      end

      def parse_sheet(xml, after:, upto:)
        doc = Ox.parse(xml)
        records = []

        doc.locate("*/sheetData/row").each do |row|
          cells_by_col = {}
          serial = nil

          row.locate("c").each do |cell|
            ref = cell["r"]
            next unless ref

            col_letters = ref.match(/\A([A-Z]+)/)[1]
            col_index = column_index(col_letters)
            cell_type = cell["t"]
            v_node = cell.locate("v").first
            next unless v_node

            text = v_node.text
            next if text.nil? || text.empty?

            if col_index == 0
              # Date cell — numeric only; shared-string headers in column A are skipped.
              next if cell_type == "s"

              serial = Integer(text, exception: false) || Float(text, exception: false)&.to_i
            elsif MID_COLUMN_RANGE.cover?(col_index)
              # MID block — skip shared-string holiday/closure flags.
              next if cell_type == "s"

              value = Float(text, exception: false)
              cells_by_col[col_index] = value if value
            end
          end

          next unless serial

          date = EXCEL_EPOCH + serial
          next if after && date < after
          next if upto && date > upto

          MID_COLUMN_RANGE.each_with_index do |col_index, quote_index|
            value = cells_by_col[col_index]
            next unless value
            next if value.zero?

            records << {
              date: date,
              base: "TOP",
              quote: QUOTE_CURRENCIES[quote_index],
              rate: value,
            }
          end
        end

        records
      end

      # Convert spreadsheet column letters (A, B, ..., Z, AA, AB, ...) to zero-based index.
      def column_index(letters)
        letters.each_char.reduce(0) { |acc, ch| acc * 26 + (ch.ord - "A".ord + 1) } - 1
      end
    end
  end
end
