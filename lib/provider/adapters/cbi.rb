# frozen_string_literal: true

require "date"
require "nokogiri"
require "ox"
require "zip"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Central Bank of Iraq (CBI). Publishes daily reference exchange rates for
    # the Iraqi dinar (IQD) against ~16 currencies plus gold via an XLSX file
    # linked from page/144 on cbi.iq.
    #
    # The page hosts three XLSX files (USD-only, multi-currency daily, and a
    # 1995-present historical archive of foreign currencies against USD). We
    # consume the multi-currency daily file: it gives us IQD-pivoted rates back
    # to 2009 for all currencies CBI publishes. The other two are USD-pivoted
    # or USD-only and redundant for our IQD-pivoted shape.
    #
    # File URLs (e.g. file-177909880482668.xlsx) rotate when CBI re-uploads,
    # so the adapter scrapes page/144 each fetch and picks the .xlsx link whose
    # anchor text mentions gold (the multi-currency daily file).
    #
    # The XLSX file uses one sheet per calendar year (2009-present). The
    # column layout has evolved over time: 2-column Buy/Sell groups in
    # 2009-2024, and 3-column Buy/Sell/Sell2 groups in 2025+. CBI publishes
    # buy and sell prices; we coerce to mid via the average of buy and sell
    # and ignore the secondary sell column where present.
    #
    # Rates are returned in CBI's native direction: foreign currency as base,
    # IQD as quote (e.g. 1 USD = ~1310 IQD), matching the convention used by
    # other pivot-in-quote adapters (NBG, BBK).
    class CBI < Adapter
      PAGE_URL = "https://cbi.iq/page/144"

      # Currency code aliases for labels CBI uses that aren't ISO 4217.
      # SDR (CBI's label) maps to XDR (ISO 4217 code for Special Drawing Rights).
      CODE_ALIASES = {
        "S.FR" => "CHF",
        "UAE" => "AED",
        "SDR" => "XDR",
        "Gold" => "XAU",
      }.freeze

      # Arabic for "gold" appears only in the multi-currency daily file's link text.
      # USD-only and historical archive links don't mention gold, giving us a stable
      # selector that survives re-ordering or new files added to the page.
      GOLD_MARKER = "الذهب"

      RELS_NS = { "r" => "http://schemas.openxmlformats.org/package/2006/relationships" }.freeze

      def fetch(after: nil, upto: nil)
        page = http.get(PAGE_URL).to_s
        url = discover_file_url(page)
        xlsx = http.get(url).to_s

        records = parse(xlsx)
        records.select! { |r| r[:date] > after } if after
        records.select! { |r| r[:date] <= upto } if upto
        records
      end

      def parse(xlsx)
        records = []

        Zip::File.open_buffer(xlsx) do |zip|
          shared_strings = read_shared_strings(zip)
          sheet_targets = read_sheet_targets(zip)

          sheet_targets.each do |name, target|
            year = Integer(name.strip, exception: false)
            next unless year

            entry = zip.find_entry("xl/#{target}")
            next unless entry

            sheet_xml = entry.get_input_stream.read
            records.concat(parse_sheet(sheet_xml, shared_strings, year))
          end
        end

        records
      end

      private

      def discover_file_url(page)
        doc = Nokogiri::HTML.parse(page)

        xlsx_anchors = doc.css("a").select do |a|
          href = a["href"]
          href&.match?(%r{\Ahttps?://.+\.xlsx\z})
        end
        raise "CBI: no XLSX links found on page/144" if xlsx_anchors.empty?

        match = xlsx_anchors.find { |a| a.text.include?(GOLD_MARKER) }
        raise "CBI: could not find multi-currency daily XLSX on page/144" unless match

        match["href"]
      end

      def parse_sheet(xml, shared_strings, year)
        rows = parse_rows(xml, shared_strings)
        layout = detect_layout(rows)
        return [] unless layout

        records = []
        current_month = nil
        rows.each do |row|
          a = row[:cells]["A"]
          if a.is_a?(String) && (month = month_from_label(a))
            current_month = month
            next
          end
          next if current_month.nil?

          day = Integer(a.to_s, exception: false)
          next unless day&.between?(1, 31)

          date = safe_date(year, current_month, day)
          next unless date

          layout.each do |buy_col, sell_col, code|
            buy = Float(row[:cells][buy_col].to_s, exception: false)
            sell = Float(row[:cells][sell_col].to_s, exception: false)
            next unless buy && sell && buy.positive? && sell.positive?

            mid = (buy + sell) / 2.0
            records << { date:, base: code, quote: "IQD", rate: mid }
          end
        end
        records
      end

      def detect_layout(rows)
        bs_row = rows.find { |r| r[:cells].values.any? { |v| v.is_a?(String) && v.include?("Buy") } }
        return unless bs_row

        buy_cols = bs_row[:cells].select { |_, v| v.is_a?(String) && v.include?("Buy") }.keys
          .sort_by { |c| col_to_num(c) }
        return if buy_cols.empty?

        sell_label = ->(s) { s.is_a?(String) && s.include?("Sell") && !s.include?("2") }
        header_rows = rows.select { |r| r[:r] < bs_row[:r] }

        buy_cols.filter_map do |buy_col|
          # Sell column is the next Sell label to the right (excluding "Sell 2")
          sell_col = bs_row[:cells]
            .select { |c, v| col_to_num(c) > col_to_num(buy_col) && sell_label.call(v) }
            .keys
            .min_by { |c| col_to_num(c) }
          next unless sell_col

          code = find_currency_code(header_rows, buy_col, sell_col)
          next unless code

          [buy_col, sell_col, code]
        end
      end

      def find_currency_code(header_rows, buy_col, sell_col)
        # Search header rows for currency code text at columns near the buy/sell group.
        # Headers can land on the buy col (e.g. 2009 layout) or any nearby col
        # (2025-2026 layout where merged headers can land on the middle col).
        buy_n = col_to_num(buy_col)
        sell_n = col_to_num(sell_col)
        search_cols = (buy_n..(sell_n + 1)).to_a.map { |n| num_to_col(n) }

        candidates = []
        header_rows.each do |row|
          search_cols.each do |c|
            text = row[:cells][c]
            next unless text.is_a?(String) && !text.empty?

            code = extract_code(text)
            candidates << code if code
          end
        end
        candidates.first
      end

      def extract_code(text)
        # ISO 4217 codes appear at the end of headers ("Saudi Arabian Riyal SAR")
        # or alone in cells ("USD", "S.FR", "Gold"). Aliased non-ISO labels
        # (S.FR, UAE, SDR, Gold) get rewritten to their ISO 4217 equivalents.
        return CODE_ALIASES[text.strip] if CODE_ALIASES.key?(text.strip)

        token = text.scan(/\b([A-Z]{3,4}|S\.FR)\b/).map(&:first).last
        return unless token

        CODE_ALIASES.fetch(token, token)
      end

      def month_from_label(label)
        # Examples seen: "Jan. 2009", "Feb.2009", "Jan,2009", "Jan. 2026"
        return unless label.is_a?(String)

        stripped = label.strip
        match = stripped.match(/\A(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)[.,\s]/i)
        return unless match
        return unless stripped.match?(/\d{4}\z/)

        {
          "jan" => 1,
          "feb" => 2,
          "mar" => 3,
          "apr" => 4,
          "may" => 5,
          "jun" => 6,
          "jul" => 7,
          "aug" => 8,
          "sep" => 9,
          "oct" => 10,
          "nov" => 11,
          "dec" => 12,
        }[match[1].downcase[0, 3]]
      end

      def safe_date(year, month, day)
        Date.new(year, month, day)
      rescue Date::Error
        nil
      end

      def read_shared_strings(zip)
        entry = zip.find_entry("xl/sharedStrings.xml")
        return [] unless entry

        doc = Ox.load(entry.get_input_stream.read, mode: :generic, effort: :tolerant)
        root = doc.respond_to?(:nodes) ? doc.nodes.find { |n| n.respond_to?(:value) && n.value == "sst" } : nil
        return [] unless root

        root.nodes.map { |si| collect_text(si) }
      end

      def read_sheet_targets(zip)
        workbook_xml = zip.find_entry("xl/workbook.xml").get_input_stream.read
        rels_xml = zip.find_entry("xl/_rels/workbook.xml.rels").get_input_stream.read

        rels = {}
        Nokogiri::XML.parse(rels_xml).xpath("//r:Relationship", RELS_NS).each do |node|
          rels[node["Id"]] = node["Target"]
        end

        workbook_doc = Ox.load(workbook_xml, mode: :generic, effort: :tolerant)
        sheets = []
        each_element(workbook_doc) do |node|
          next unless node.respond_to?(:value) && node.value == "sheet"

          name = node["name"]
          rid = node["r:id"] || node["id"]
          next unless name && rid

          target = rels[rid]
          sheets << [name, target] if target
        end
        sheets
      end

      def each_element(node, &block)
        return unless node.respond_to?(:nodes) && node.nodes

        node.nodes.each do |child|
          next unless child.respond_to?(:value)

          yield(child)
          each_element(child, &block)
        end
      end

      def collect_text(node)
        return "" unless node.respond_to?(:nodes) && node.nodes

        node.nodes.map do |child|
          if child.is_a?(String)
            ""
          elsif child.respond_to?(:value) && child.value == "t"
            child.nodes.first.to_s
          elsif child.respond_to?(:value) && child.value == "r"
            collect_text(child)
          else
            ""
          end
        end.join
      end

      def parse_rows(xml, shared_strings)
        doc = Ox.load(xml, mode: :generic, effort: :tolerant)
        sheet_data = nil
        each_element(doc) do |node|
          if node.respond_to?(:value) && node.value == "sheetData"
            sheet_data = node
            break
          end
        end
        return [] unless sheet_data

        rows = []
        sheet_data.nodes.each do |row_node|
          next unless row_node.respond_to?(:value) && row_node.value == "row"

          r = row_node["r"].to_i
          cells = {}
          row_node.nodes.each do |cell|
            next unless cell.respond_to?(:value) && cell.value == "c"

            ref = cell["r"]
            next unless ref&.match?(/\A[A-Z]+\d+\z/)

            col = ref[/\A[A-Z]+/]
            t = cell["t"]
            value = extract_cell_value(cell, t, shared_strings)
            cells[col] = value unless value.nil?
          end
          rows << { r: r, cells: cells }
        end
        rows
      end

      def extract_cell_value(cell, t, shared_strings)
        if t == "inlineStr"
          is_node = cell.nodes.find { |n| n.respond_to?(:value) && n.value == "is" }
          return unless is_node

          collect_text(is_node)
        else
          v_node = cell.nodes.find { |n| n.respond_to?(:value) && n.value == "v" }
          return unless v_node

          raw = v_node.nodes.first.to_s
          t == "s" ? shared_strings[raw.to_i] : raw
        end
      end

      def col_to_num(col)
        n = 0
        col.each_byte { |b| n = n * 26 + (b - 64) }
        n
      end

      def num_to_col(n)
        col = +""
        while n > 0
          n -= 1
          col.prepend((65 + n % 26).chr)
          n /= 26
        end
        col
      end
    end
  end
end
