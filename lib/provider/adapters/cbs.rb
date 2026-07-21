# frozen_string_literal: true

require "ox"
require "uri"
require "zip"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Central Bank of Samoa.
    #
    # Publishes the historical daily fix as a single XLSX workbook covering
    # 2008-01-02 to the most recent business day. The workbook has one sheet
    # per year ("2008" .. "2026"), and within each sheet twelve month sections
    # stacked vertically. Each section opens with a month-name banner in column
    # B, a header row of "DATE | TALA/USD | TALA/NZD | ..." beneath it, then
    # one data row per business day with an Excel serial date in column B.
    #
    # Rates are published as "Units of Foreign Currency per ST1.00", i.e. one
    # tala expressed in the quote currency (1 WST = 0.36847 USD). Records are
    # emitted in that native direction — WST in `base`, foreign in `quote` —
    # matching ECB's pivot-in-base orientation. The blender handles rebase.
    #
    # The "TALA/YEN" column maps to JPY and "TALA/EURO" to EUR. CNY and CNH
    # (onshore vs offshore yuan) only appear in recent years; the workbook adds
    # the CNH column around 2023.
    #
    # The workbook lives behind a date-stamped filename that changes every day
    # (e.g. "Historical-Daily-Rates-June032026.xlsx"), so the download link can't
    # be hardcoded. We scrape the data page on each run for its current ".xlsx"
    # link and follow it. The workbook itself is a complete archive, so a single
    # incremental backfill catches up any gap left while the link was stale.
    #
    # Page caveat: "indicative rates only but not for market use. Kindly refer
    # to the commercial banks for market exchange rates."
    class CBS < Adapter
      DATA_URL = "https://cbs.gov.ws/daily-exchange-rates"

      # Excel stores dates as days since this epoch (with the 1900 leap-year
      # quirk baked into the offset).
      EXCEL_EPOCH = Date.new(1899, 12, 30)

      # The header row above each month section uses these labels in row 2,
      # column C onward. Map each label to its ISO 4217 code.
      CURRENCIES = {
        "TALA/USD" => "USD",
        "TALA/NZD" => "NZD",
        "TALA/AUD" => "AUD",
        "TALA/EURO" => "EUR",
        "TALA/FJD" => "FJD",
        "TALA/YEN" => "JPY",
        "TALA/GBP" => "GBP",
        "TALA/CNY" => "CNY",
        "TALA/CNH" => "CNH",
      }.freeze

      MONTH_NAMES = [
        "JANUARY",
        "FEBRUARY",
        "MARCH",
        "APRIL",
        "MAY",
        "JUNE",
        "JULY",
        "AUGUST",
        "SEPTEMBER",
        "OCTOBER",
        "NOVEMBER",
        "DECEMBER",
      ].freeze

      def fetch(after: nil, upto: nil)
        records = parse(download(archive_url(download(DATA_URL))))
        records = records.select { |r| r[:date] >= after } if after
        records = records.select { |r| r[:date] <= upto } if upto
        records
      end

      # Resolve the current workbook URL from the data page HTML. The link is a
      # root-relative "/media/...xlsx" href whose filename embeds the date. The
      # filename scheme is not stable — it has shifted between hyphen- and
      # space-separated forms — so percent-encode the path before joining;
      # an unescaped space raises URI::InvalidURIError.
      def archive_url(html)
        path = html[/href=["']([^"']*\.xlsx)["']/i, 1]
        raise "no workbook link on #{DATA_URL}" unless path

        URI.join(DATA_URL, URI::RFC2396_PARSER.escape(path)).to_s
      end

      def parse(xlsx_bytes)
        records = []

        Zip::File.open_buffer(xlsx_bytes) do |zip|
          strings = shared_strings(zip)
          sheet_paths(zip).each do |path|
            sheet_xml = zip.find_entry(path).get_input_stream.read
            doc = Ox.load(sheet_xml, mode: :generic, effort: :tolerant)
            sheet_data = find_sheet_data(doc)
            next unless sheet_data

            parse_sheet(sheet_data.nodes, strings, records)
          end
        end

        records
      end

      private

      # Walk all sheet*.xml files in the workbook. CBS uses one sheet per year,
      # but the per-year ordering is not lexicographic (sheet1 holds 2008,
      # sheet19 holds 2026), so we collect everything and let the date filter
      # downstream sort it out.
      def sheet_paths(zip)
        zip.entries
          .map(&:name)
          .grep(%r{\Axl/worksheets/sheet\d+\.xml\z})
          .sort
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

      def parse_sheet(rows, strings, records)
        column_map = {}

        rows.each do |row|
          first_label = string_cell(row, "B", strings)

          if first_label == "DATE"
            column_map = build_column_map(row, strings)
            next
          end

          # Month banner row — skip but reset map so a malformed section
          # cannot leak headers across months.
          if first_label && MONTH_NAMES.include?(first_label.upcase)
            column_map = {}
            next
          end

          next if column_map.empty?

          date = date_cell(row, "B")
          next unless date

          row.nodes.each do |cell|
            ref = cell["r"]
            next unless ref

            column = ref.sub(/\d+\z/, "")
            iso = column_map[column]
            next unless iso

            value = cell_numeric_value(cell)
            next unless value

            records << { date: date, base: "WST", quote: iso, rate: value }
          end
        end
      end

      def build_column_map(row, strings)
        map = {}
        row.nodes.each do |cell|
          ref = cell["r"]
          next unless ref

          column = ref.sub(/\d+\z/, "")
          next if column == "B"

          label = cell_string_value(cell, strings)
          next unless label

          iso = CURRENCIES[label.upcase]
          map[column] = iso if iso
        end
        map
      end

      def string_cell(row, column, strings)
        cell = row.nodes.find { |c| c["r"]&.sub(/\d+\z/, "") == column }
        return unless cell

        cell_string_value(cell, strings)
      end

      def cell_string_value(cell, strings)
        return unless cell["t"] == "s"

        value = cell.nodes.find { |n| n.respond_to?(:value) && n.value == "v" }
        return unless value

        strings[value.nodes.first.to_s.to_i]
      end

      def date_cell(row, column)
        cell = row.nodes.find { |c| c["r"]&.sub(/\d+\z/, "") == column }
        return unless cell
        return if cell["t"] == "s"

        value = cell.nodes.find { |n| n.respond_to?(:value) && n.value == "v" }
        return unless value

        serial = value.nodes.first.to_s.to_f
        return if serial <= 30_000 || serial >= 80_000

        EXCEL_EPOCH + serial.to_i
      end

      def cell_numeric_value(cell)
        return if cell["t"] == "s"

        value = cell.nodes.find { |n| n.respond_to?(:value) && n.value == "v" }
        return unless value

        text = value.nodes.first.to_s.strip
        return if text.empty?

        rate = Float(text, exception: false)
        return unless rate&.positive?

        rate
      end

      def shared_strings(zip)
        entry = zip.find_entry("xl/sharedStrings.xml")
        return [] unless entry

        doc = Ox.load(entry.get_input_stream.read, mode: :generic, effort: :tolerant)
        root = doc.nodes.first
        return [] unless root

        root.nodes.map do |si|
          si.nodes.map { |t| t.nodes.first.to_s }.join
        end
      end

      def download(url)
        http.get(url).to_s
      end

      def read_timeout = 120
    end
  end
end
