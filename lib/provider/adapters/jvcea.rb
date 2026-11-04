# frozen_string_literal: true

require "ox"
require "zip"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Japan Virtual and Crypto assets Exchange Association (JVCEA). Certified self-regulatory organization under Japan's
    # Payment Services Act and Financial Instruments and Exchange Act. Publishes daily JPY spot reference prices
    # ("参考価格") calculated as the volume-weighted average across regulated domestic exchanges during the 30-minute window
    # around 21:00 JST (20:45 to 21:15 JST), released the next business day at 17:00 JST.
    #
    # Each pair is served as a full-history XLSX workbook from 2020-05-01 onwards. Sheet 1 ("取引所") carries the exchange
    # spot reference price in column F, with the Excel serial date in column B and the pair code in column C. Days
    # without spot trades carry the shared string "none" in column F and are skipped. Post-publication corrections are
    # made in place and flagged with "※" in column A.
    #
    # Terms: https://jvcea.or.jp/statistics/refer_rate/
    class JVCEA < Adapter
      API_URL = "https://rf-dat.jvcea.or.jp/ReferRate/referratefile"
      EXCEL_EPOCH = Date.new(1899, 12, 30)
      PAIRS = ["BTC", "BCH", "ETH", "LTC", "SOL", "XRP"].freeze

      class << self
        def revises? = true
      end

      def fetch(after: nil, upto: nil)
        return [] if after && upto && after > upto

        PAIRS.each_with_index.flat_map do |base, index|
          sleep(0.2) unless index.zero?
          xlsx = http.get(API_URL, params: { pair: "#{base}JPY" }).to_s
          parse(xlsx, after:, upto:)
        end
      end

      def parse(xlsx, after: nil, upto: nil)
        strings, sheet = read_workbook(xlsx)
        doc = Ox.load(sheet, mode: :generic, effort: :tolerant)
        rows = doc.locate("*/sheetData/row")
        raise "JVCEA: sheetData missing from workbook" if rows.empty?

        rows.filter_map do |row|
          cells = row.locate("c").to_h { |cell| [cell["r"].to_s.sub(/\d+\z/, ""), cell] }
          parse_row(cells, strings, after:, upto:)
        end
      end

      private

      def parse_row(cells, strings, after:, upto:)
        date_cell = cells["B"]
        pair_cell = cells["C"]
        rate_cell = cells["F"]
        return unless date_cell && pair_cell && rate_cell
        return if date_cell["t"] == "s" || rate_cell["t"] == "s"

        serial = Float(cell_text(date_cell), exception: false)&.to_i
        return unless serial&.positive?

        date = EXCEL_EPOCH + serial
        return if after && date < after
        return if upto && date > upto

        pair = cell_string(pair_cell, strings)
        return unless pair&.end_with?("JPY")

        rate = Float(cell_text(rate_cell), exception: false)
        return unless rate&.positive?

        { date:, base: pair.delete_suffix("JPY"), quote: "JPY", rate: }
      end

      def read_workbook(xlsx)
        sheet = nil
        strings_xml = nil

        Zip::File.open_buffer(xlsx) do |zip|
          sheet = zip.find_entry("xl/worksheets/sheet1.xml")&.get_input_stream&.read
          strings_xml = zip.find_entry("xl/sharedStrings.xml")&.get_input_stream&.read
        end
        raise "JVCEA: sheet1.xml missing from workbook" unless sheet

        [shared_strings(strings_xml), sheet]
      end

      def shared_strings(xml)
        raise "JVCEA: sharedStrings.xml missing from workbook" unless xml

        doc = Ox.load(xml, mode: :generic, effort: :tolerant)
        root = doc.is_a?(Ox::Document) ? doc.nodes.find { |n| n.is_a?(Ox::Element) } : doc
        raise "JVCEA: sharedStrings.xml has no root element" unless root

        root.locate("si").map { |si| si.locate("t").map { |t| t.text.to_s }.join }
      end

      def cell_text(cell)
        cell.locate("v").first&.text.to_s
      end

      def cell_string(cell, strings)
        text = cell_text(cell)
        cell["t"] == "s" ? strings[text.to_i] : text
      end
    end
  end
end
