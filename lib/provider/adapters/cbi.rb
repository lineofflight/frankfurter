# frozen_string_literal: true

require "date"
require "net/http"
require "zlib"

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
    # so the adapter scrapes page/144 each fetch and picks the second .xlsx
    # link (the multi-currency daily file).
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
      USER_AGENT = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " \
        "(KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

      # Currency code aliases for labels CBI uses that aren't ISO 4217.
      # SDR (CBI's label) maps to XDR (ISO 4217 code for Special Drawing Rights).
      CODE_ALIASES = {
        "S.FR" => "CHF",
        "UAE" => "AED",
        "SDR" => "XDR",
        "Gold" => "XAU",
      }.freeze

      def fetch(after: nil, upto: nil)
        page = http_get(URI(PAGE_URL))
        url = discover_file_url(page)
        xlsx = http_get(URI(url))

        records = parse(xlsx)
        records.select! { |r| r[:date] > after } if after
        records.select! { |r| r[:date] <= upto } if upto
        records
      end

      def parse(xlsx)
        reader = ZipReader.new(xlsx)
        shared_strings = parse_shared_strings(reader.read("xl/sharedStrings.xml"))
        workbook = reader.read("xl/workbook.xml")
        rels = reader.read("xl/_rels/workbook.xml.rels")

        records = []
        sheet_names(workbook).each do |name, rid|
          year = Integer(name.strip, exception: false)
          next unless year

          target = rels[/Id="#{rid}"[^>]*?Target="([^"]+)"/, 1]
          next unless target

          sheet_xml = reader.read("xl/#{target}")
          records.concat(parse_sheet(sheet_xml, shared_strings, year))
        end
        records
      end

      private

      def http_get(uri)
        http = Net::HTTP.new(uri.host, uri.port)
        http.use_ssl = uri.scheme == "https"
        http.open_timeout = 30
        http.read_timeout = 120

        req = Net::HTTP::Get.new(uri)
        req["User-Agent"] = USER_AGENT
        req["Accept"] = "*/*"

        response = http.request(req)
        response.value
        response.body
      end

      # Arabic for "gold" appears only in the multi-currency daily file's link text.
      # USD-only and historical archive links don't mention gold, giving us a stable
      # selector that survives re-ordering or new files added to the page.
      GOLD_MARKER = "الذهب"

      def discover_file_url(page)
        html = page.dup.force_encoding(Encoding::UTF_8)
        anchors = html.scan(%r{<a\s+href="(https?://[^"]+\.xlsx)"[^>]*>(.*?)</a>}m)
        raise "CBI: no XLSX links found on page/144" if anchors.empty?

        match = anchors.find { |_, label| label.include?(GOLD_MARKER) }
        raise "CBI: could not find multi-currency daily XLSX on page/144" unless match

        match.first
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

        match = label.match(/\A\s*(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\.?\s*[,\s]?\s*\d{4}/i)
        return unless match

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

      def sheet_names(workbook_xml)
        workbook_xml.scan(/<sheet\s+name="([^"]+)"\s+sheetId="[^"]*"\s+r:id="([^"]+)"/).to_a
      end

      def parse_shared_strings(xml)
        xml.scan(%r{<si\b[^>]*>(.*?)</si>}m).map do |inner,|
          text = inner.scan(%r{<t\b[^>]*>([^<]*)</t>}m).join
          text.gsub("&amp;", "&").gsub("&lt;", "<").gsub("&gt;", ">").gsub("&quot;", '"').gsub("&apos;", "'")
        end
      end

      def parse_rows(xml, shared_strings)
        rows = []
        xml.scan(%r{<row\b([^>]*)>(.*?)</row>}m).each do |attrs, content|
          r = attrs[/\br="(\d+)"/, 1].to_i
          cells = {}
          content.scan(%r{<c\b([^/]*?)(?:/>|>(.*?)</c>)}m).each do |cell_attrs, body|
            ref = cell_attrs[/\br="([A-Z]+\d+)"/, 1]
            next unless ref

            col = ref[/\A[A-Z]+/]
            t = cell_attrs[/\bt="([^"]+)"/, 1]

            v = nil
            if body
              v = body[%r{<v>([^<]*)</v>}, 1]
              v ||= body[%r{<is><t[^>]*>([^<]*)</t></is>}, 1] if t == "inlineStr"
            end
            next unless v

            cells[col] = t == "s" ? shared_strings[v.to_i] : v
          end
          rows << { r: r, cells: cells }
        end
        rows
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

      # Minimal ZIP reader (XLSX is a ZIP container of XML files). Uses
      # zlib's raw inflate; rubyzip would pull in another gem for trivial work.
      class ZipReader
        LOCAL_HEADER_SIG = 0x04034b50
        CENTRAL_DIR_SIG = 0x02014b50
        EOCD_SIG = "\x50\x4b\x05\x06".b

        def initialize(data)
          @data = data.dup.force_encoding(Encoding::BINARY)
          @entries = read_central_directory
        end

        def read(name)
          entry = @entries.fetch(name) { raise "ZipReader: entry not found: #{name}" }
          sig, _ver, _flags, method, _mtime, _mdate, _crc, _csize, _usize, fname_len, extra_len =
            @data[entry[:offset], 30].unpack("VvvvvvVVVvv")
          raise "ZipReader: bad local header for #{name}" unless sig == LOCAL_HEADER_SIG

          start = entry[:offset] + 30 + fname_len + extra_len
          compressed = @data[start, entry[:csize]]

          case method
          when 0 then compressed
          when 8 then Zlib::Inflate.new(-Zlib::MAX_WBITS).inflate(compressed)
          else raise "ZipReader: unsupported compression #{method}"
          end
        end

        private

        def read_central_directory
          eocd = @data.rindex(EOCD_SIG)
          raise "ZipReader: no EOCD record" unless eocd

          cd_entries, _cd_size, cd_offset = @data[eocd + 10, 12].unpack("vVV")
          entries = {}
          pos = cd_offset
          cd_entries.times do
            unpacked = @data[pos, 46].unpack("VvvvvvvVVVvvvvvVV")
            sig = unpacked[0]
            csize = unpacked[8]
            fname_len = unpacked[10]
            extra_len = unpacked[11]
            comment_len = unpacked[12]
            local_offset = unpacked[16]
            raise "ZipReader: bad central directory entry" unless sig == CENTRAL_DIR_SIG

            name = @data[pos + 46, fname_len].force_encoding(Encoding::UTF_8)
            entries[name] = { offset: local_offset, csize: csize }
            pos += 46 + fname_len + extra_len + comment_len
          end
          entries
        end
      end
    end
  end
end
