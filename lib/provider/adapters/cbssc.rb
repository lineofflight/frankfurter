# frozen_string_literal: true

require "oj"
require "openssl"
require "ox"
require "zip"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Central Bank of Seychelles. Publishes the Consolidated Average Rates of Authorised Dealers: a daily buy, sell and
    # mid rate for USD, EUR and GBP against the Seychellois rupee (SCR), expressed as rupees per unit of foreign
    # currency (1 USD = 14.6 SCR). The pivot sits in `quote`, as with NBG.
    #
    # Two sources, both carrying the mid rate:
    #
    # - A statistics workbook ("Exchange Rates-Daily.xlsx") with one sheet per currency, a "Date | SCR/USD" header row
    #   and one row per business day from 2000-01-04, regenerated daily around 12:00 UTC. The FXArchivedRates.jsp feed
    #   the site itself reads only reaches back to 2012, carries buy/sell alone and lags the live day by about two
    #   months, so the workbook is the archive.
    # - The live CAR endpoint, which has only the current day. It runs ahead of the workbook by a day, so it fills the
    #   head of the series.
    #
    # The workbook stores the raw dealer average to full float precision (14.616577838181803) while every published view
    # of the same figure (the CAR page, the archived feed) shows four decimals. Round to four so a day caught live and
    # the same day read from the workbook agree.
    #
    # TLS quirk: www.cbs.sc serves only its leaf certificate, so the default trust store can't build a chain to a root.
    # We bundle the Sectigo intermediate at config/cbssc_ca_bundle.pem and pass it via an explicit ssl_context on each
    # request instead of disabling verification, as BOA does.
    class CBSSC < Adapter
      ARCHIVE_URL = "https://www.cbs.sc/Downloads/StaExcel/Exchange%20Rates-Daily.xlsx"
      LIVE_URL = "https://www.cbs.sc/Controller/MarketinfoController.jsp"
      CA_BUNDLE = File.expand_path("../../../config/cbssc_ca_bundle.pem", __dir__)

      EXCEL_EPOCH = Date.new(1899, 12, 30)

      HEADER = %r{\ASCR/([A-Z]{3})\z}

      def fetch(after: nil, upto: nil)
        records = parse(download(ARCHIVE_URL)) + parse_live(download(LIVE_URL, type: "car"))
        records = records.select { |r| r[:date] >= after } if after
        records = records.select { |r| r[:date] <= upto } if upto
        records.uniq { |r| [r[:date], r[:base]] }
      end

      def parse(xlsx_bytes)
        records = []

        Zip::File.open_buffer(xlsx_bytes) do |zip|
          strings = shared_strings(zip)
          sheet_paths(zip).each do |path|
            doc = Ox.load(zip.find_entry(path).get_input_stream.read, mode: :generic, effort: :tolerant)
            sheet_data = find_sheet_data(doc)
            next unless sheet_data

            parse_sheet(sheet_data.nodes, strings, records)
          end
        end

        raise "CBSSC: no rates in workbook at #{ARCHIVE_URL}" if records.empty?

        records
      end

      def parse_live(json)
        data = Oj.load(json, mode: :strict)
        row = data["car"]&.first
        raise "CBSSC: no CAR rates in live response" unless row

        date = Date.strptime(row.fetch("currentDate"), "%d-%b-%Y")

        ["USD", "EUR", "GBP"].filter_map do |iso|
          rate = Float(row.fetch("#{iso.downcase}mid"))
          next if rate.zero?

          { date:, base: iso, quote: "SCR", rate: }
        end
      end

      private

      def download(url, **params)
        http.get(url, params:, ssl_context:).to_s
      end

      def ssl_context
        @ssl_context ||= OpenSSL::SSL::SSLContext.new.tap do |ctx|
          store = OpenSSL::X509::Store.new
          store.set_default_paths
          store.add_file(CA_BUNDLE)
          ctx.set_params(cert_store: store)
        end
      end

      def sheet_paths(zip)
        zip.entries.map(&:name).grep(%r{\Axl/worksheets/sheet\d+\.xml\z}).sort
      end

      def find_sheet_data(node)
        return node if node.respond_to?(:value) && node.value == "sheetData"
        return unless node.respond_to?(:nodes)

        node.nodes.each do |child|
          found = find_sheet_data(child)
          return found if found
        end
        nil
      end

      # Each currency sheet opens with a "Date | SCR/USD" header; rows beneath carry an Excel serial in A and the mid in
      # B. Sheets without such a header (the notes, the hidden yearly averages) emit nothing. A day with no fixing holds
      # a text placeholder in B instead of a number (GBP on 2020-04-09), which numeric_cell skips.
      def parse_sheet(rows, strings, records)
        iso = nil

        rows.each do |row|
          iso ||= string_cell(row, "B", strings)&.[](HEADER, 1)
          next unless iso

          serial = numeric_cell(row, "A")
          rate = numeric_cell(row, "B")
          next unless serial && rate&.positive?
          next if serial <= 30_000 || serial >= 80_000

          records << { date: EXCEL_EPOCH + serial.to_i, base: iso, quote: "SCR", rate: rate.round(4) }
        end
      end

      def cell(row, column)
        row.nodes.find { |c| c["r"]&.sub(/\d+\z/, "") == column }
      end

      def cell_value(cell)
        value = cell.nodes.find { |n| n.respond_to?(:value) && n.value == "v" }
        value&.nodes&.first.to_s.strip
      end

      def string_cell(row, column, strings)
        c = cell(row, column)
        return unless c && c["t"] == "s"

        strings[cell_value(c).to_i]
      end

      def numeric_cell(row, column)
        c = cell(row, column)
        return unless c && c["t"] != "s"

        Float(cell_value(c), exception: false)
      end

      def shared_strings(zip)
        entry = zip.find_entry("xl/sharedStrings.xml")
        raise "CBSSC: xl/sharedStrings.xml missing from workbook" unless entry

        doc = Ox.load(entry.get_input_stream.read, mode: :generic, effort: :tolerant)
        root = doc.nodes.first
        raise "CBSSC: sharedStrings.xml has no root element" unless root

        root.nodes.map { |si| si.nodes.map { |t| t.nodes.first.to_s }.join }
      end
    end
  end
end
