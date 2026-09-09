# frozen_string_literal: true

require "json"
require "ox"
require "uri"
require "zip"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Bank of Zambia.
    #
    # Publishes the daily average exchange rate as a single XLSX workbook carrying the whole series from 2006. The
    # workbook has one sheet: a currency banner in row 3 ("Dollar", "Pound", "Euro", "Rand"), a header row beneath it
    # ("Date | Buy | Sale | Buy | Sale ..."), then one data row per business day with an Excel serial date in column B
    # and a buy/sell pair per currency. Rates are kwacha per unit of foreign currency (1 USD = 19.20 ZMW), so records
    # are emitted with the foreign currency in `base` and the kwacha in `quote`, matching the pivot-in-quote pattern of
    # NBG and BBK. Each rate is the midpoint of the published buy and sell.
    #
    # The kwacha was rebased on 2013-01-01 at 1000 ZMK = 1 ZMW. The workbook does not restate earlier rows, which keep
    # their old-kwacha values (5136 per USD on 2012-12-28, 5.21 on 2013-01-02), so rows before the changeover are
    # labelled ZMK and everything after ZMW. The first seven rows (2006-01-03 to 2006-01-11) are hidden in the workbook
    # and mostly hold placeholder text and stray formulas; hidden rows are skipped, so coverage starts on 2006-01-12.
    #
    # The public site is an Angular SPA whose bundle hardcodes a Drupal JSON:API on the same host. A new
    # historical_average_exchange_rate node is created each business day, each attached to a freshly uploaded copy of
    # the workbook under a changing filename (AVERAGE_FXRATES_12.xlsx, _13.xlsx, ...), so the download link cannot be
    # hardcoded. We ask the API for the most recently created node with its file included and follow the file URI.
    #
    # The bank also serves an intraday USD/ZMW view (three fixes per day since 2026-01) at
    # /api/v1/views/boz_zmw_usd_daily_exchange_rates. That is a different series and is not consumed here.
    class BOZ < Adapter
      SITE_URL = "https://www.boz.zm"
      API_URL = "#{SITE_URL}/jsonapi/node/historical_average_exchange_rate".freeze
      API_PARAMS = { include: "field_average_historical_file", sort: "-created", "page[limit]" => 1 }.freeze

      # Excel stores dates as days since this epoch (with the 1900 leap-year quirk baked into the offset).
      EXCEL_EPOCH = Date.new(1899, 12, 30)

      # First date the rebased kwacha applies. Earlier rows price the old kwacha.
      REDENOMINATION = Date.new(2013, 1, 1)

      # Banner labels in row 3 (whitespace-padded in the source) to ISO 4217 codes.
      CURRENCIES = {
        "DOLLAR" => "USD",
        "POUND" => "GBP",
        "EURO" => "EUR",
        "RAND" => "ZAR",
      }.freeze

      def fetch(after: nil, upto: nil)
        records = parse(download(workbook_url(download(API_URL, params: API_PARAMS))))
        records = records.select { |r| r[:date] >= after } if after
        records = records.select { |r| r[:date] <= upto } if upto
        records
      end

      # Resolve the workbook URL from the JSON:API listing: the first node's file relationship points into the
      # `included` array, whose entry carries the root-relative file URI.
      def workbook_url(json)
        doc = JSON.parse(json)
        node = doc.fetch("data", []).first
        raise "BOZ: no historical_average_exchange_rate node at #{API_URL}" unless node

        file_id = node.dig("relationships", "field_average_historical_file", "data", "id")
        file = doc.fetch("included", []).find { |entry| entry["id"] == file_id }
        path = file&.dig("attributes", "uri", "url")
        raise "BOZ: node #{node["id"]} has no workbook attached" unless path

        URI.join(SITE_URL, path).to_s
      end

      def parse(xlsx_bytes)
        records = []

        Zip::File.open_buffer(xlsx_bytes) do |zip|
          strings = shared_strings(zip)
          entry = zip.find_entry("xl/worksheets/sheet1.xml")
          raise "BOZ: xl/worksheets/sheet1.xml missing from workbook" unless entry

          doc = Ox.load(entry.get_input_stream.read, mode: :generic, effort: :tolerant)
          sheet_data = find_sheet_data(doc)
          raise "BOZ: no sheetData in workbook" unless sheet_data

          parse_sheet(sheet_data.nodes, strings, records)
        end

        records
      end

      private

      def find_sheet_data(node)
        return node if node.respond_to?(:value) && node.value == "sheetData"
        return unless node.respond_to?(:nodes)

        node.nodes.each do |child|
          found = find_sheet_data(child)
          return found if found
        end
        nil
      end

      # Two header rows precede the data: the currency banner, whose labels sit over each currency's Buy column, and the
      # "Date | Buy | Sale" row that confirms the pairing. Columns are resolved from both, so a reshuffled workbook
      # fails loudly instead of pairing the wrong cells.
      def parse_sheet(rows, strings, records)
        banner = {}
        columns = nil

        rows.each do |row|
          if columns.nil?
            labels = string_cells(row, strings)

            if labels["B"] == "DATE"
              columns = pair_columns(banner, labels)
            else
              labels.each { |column, label| banner[column] = CURRENCIES[label] if CURRENCIES[label] }
            end
            next
          end

          next if row["hidden"] == "1"

          date = date_cell(row, "B")
          next unless date

          quote = date < REDENOMINATION ? "ZMK" : "ZMW"

          columns.each do |iso, (buy_column, sell_column)|
            buy = numeric_cell(row, buy_column)
            sell = numeric_cell(row, sell_column)
            next unless buy && sell

            records << { date: date, base: iso, quote: quote, rate: midpoint(buy, sell) }
          end
        end

        raise "BOZ: no header row in workbook" unless columns
      end

      def pair_columns(banner, labels)
        raise "BOZ: no currency banner above the header row" if banner.empty?

        banner.to_h do |buy_column, iso|
          sell_column = buy_column.succ
          unless labels[buy_column] == "BUY" && labels[sell_column] == "SALE"
            raise "BOZ: expected Buy/Sale under #{iso} at #{buy_column}/#{sell_column}"
          end

          [iso, [buy_column, sell_column]]
        end
      end

      def string_cells(row, strings)
        row.nodes.each_with_object({}) do |cell, labels|
          next unless cell["r"] && cell["t"] == "s"

          value = value_node(cell)
          next unless value

          labels[column_of(cell)] = strings[value.to_i].strip.upcase
        end
      end

      def date_cell(row, column)
        cell = find_cell(row, column)
        return unless cell
        return if cell["t"] == "s"

        serial = value_node(cell)
        return unless serial

        serial = serial.to_f
        return if serial <= 30_000 || serial >= 80_000

        EXCEL_EPOCH + serial.to_i
      end

      # The stored text, untouched, so the midpoint sees the published digits rather than a float round-trip.
      def numeric_cell(row, column)
        cell = find_cell(row, column)
        return unless cell
        return if cell["t"] == "s"

        text = value_node(cell)&.strip
        return if text.nil? || text.empty?

        rate = Float(text, exception: false)
        return unless rate&.positive?

        text
      end

      def find_cell(row, column)
        row.nodes.find { |cell| cell["r"] && column_of(cell) == column }
      end

      def column_of(cell)
        cell["r"].sub(/\d+\z/, "")
      end

      def value_node(cell)
        value = cell.nodes.find { |n| n.respond_to?(:value) && n.value == "v" }
        return unless value

        value.nodes.first&.to_s
      end

      def shared_strings(zip)
        entry = zip.find_entry("xl/sharedStrings.xml")
        raise "BOZ: xl/sharedStrings.xml missing from workbook" unless entry

        doc = Ox.load(entry.get_input_stream.read, mode: :generic, effort: :tolerant)
        root = doc.nodes.first
        raise "BOZ: sharedStrings.xml has no root element" unless root

        root.nodes.map do |si|
          si.nodes.map { |t| t.nodes.first.to_s }.join
        end
      end

      def download(url, params: nil)
        http.get(url, params: params).to_s
      end
    end
  end
end
