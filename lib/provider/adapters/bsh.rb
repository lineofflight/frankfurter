# frozen_string_literal: true

require "net/http"
require "nokogiri"
require "ox"
require "zip"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Bank of Albania (Banka e Shqipërisë).
    #
    # Two-track adapter:
    #
    # 1. Live HTML page — latest daily fix (and a weekly minor-currency fix).
    #    The page renders two tables, each prefixed by "Last update: DD.MM.YYYY".
    #    Used for current/recent dates when an `after` cutoff is near today.
    #
    # 2. Per-year XLSX archives — historical backfill from 2013 onward. Each
    #    workbook has one sheet per month (Janar, Shkurt, ...). Layout is
    #    transposed vs. the live page: dates run across the columns (every 3rd
    #    column carries a "DT.DD.MM.YYYY" header), currencies down the rows.
    #    For each currency row, the cell immediately to the right of a date
    #    header holds the rate "Lek per unit of foreign" (ALL/foreign).
    #
    # Pre-2013 archive files are legacy binary .xls (BIFF) workbooks that
    # rubyzip/Ox can't open, so 2013-01-03 is the practical earliest XLSX date.
    #
    # Rates follow the "1 unit foreign = X ALL" convention, matching the NBG
    # and BBK pivot-in-quote orientation. JPY is published per 100 units (the
    # row label literally reads "(per 100)") and divided down to per-1-unit.
    # XAU/XAG are published per troy ounce, matching the in-app convention.
    # SDR is dropped because it is a composite unit; Money does not register it.
    class BSh < Adapter
      LIVE_URL = "https://www.bankofalbania.org/Markets/Official_exchange_rate/"
      ARCHIVE_INDEX_URL = "https://www.bankofalbania.org/Markets/Official_exchange_rate/Exchage_rate_archive/"
      ARCHIVE_BASE_URL = "https://www.bankofalbania.org"
      USER_AGENT = "Mozilla/5.0 (compatible; Frankfurter/2.0; +https://frankfurter.dev)"

      # Date format used in the "Last update: DD.MM.YYYY" markers preceding
      # each table on the live page.
      DATE_PATTERN = /\A\d{2}\.\d{2}\.\d{4}\z/

      # Date markers in archive worksheets: "DT.DD.MM.YYYY" (with various
      # spacing quirks the source introduces year by year).
      ARCHIVE_DATE_PATTERN = /DT\.?\s*(\d{1,2})\.(\d{1,2})\.(\d{4})/

      # ISO 4217 code embedded at the end of the Albanian currency labels,
      # e.g. "Dollari Amerikan (USD)", "Jeni Japonez (per 100) (JPY)", or with
      # no parens like "Juani Kinez (onshore) CNY".
      ARCHIVE_CODE_PATTERN = /(?:\(|\s)([A-Z]{3})\)?\s*\z/

      # Some Albanian labels carry no ISO code at all — "E U R O" for the
      # euro, "Ari (oz)" for gold, "Argjendi (oz)" for silver. Map them
      # explicitly. Keys are normalised (lowercase, single-space, stripped).
      ARCHIVE_LABEL_OVERRIDES = {
        "e u r o" => "EUR",
        "ari (oz)" => "XAU",
        "argjendi (oz)" => "XAG",
      }.freeze

      # Quotes published per N units in the source. Divide by N to normalise
      # to a per-1-unit rate.
      UNIT_MULTIPLIERS = {
        "JPY" => 100,
      }.freeze

      # SDR is a composite unit and not an ISO 4217 currency we surface.
      EXCLUDED_QUOTES = ["SDR"].freeze

      # Archive ingestion only goes back to 2013 (the first .xlsx year).
      ARCHIVE_MIN_YEAR = 2013

      # Days of recency to keep using the live HTML page. Beyond this window
      # we fall back to the per-year XLSX archive.
      LIVE_WINDOW_DAYS = 30

      class << self
        # Chunk historical backfill by year so each fetch maps to one archive
        # file. The base class iterates `after` forward by this many days.
        def backfill_range = 365
      end

      def fetch(after: nil, upto: nil)
        records = {}

        if use_live?(after:)
          parse_live(load_page(LIVE_URL)).each do |record|
            next if after && record[:date] < after
            next if upto && record[:date] > upto

            records[record_key(record)] = record
          end
        end

        archive_years(after:, upto:).each do |year|
          url = archive_urls[year]
          next unless url

          parse_archive(load_binary(url)).each do |record|
            next if after && record[:date] < after
            next if upto && record[:date] > upto

            # Live-page records take precedence on overlap — they reflect the
            # most recently published fix.
            key = record_key(record)
            records[key] ||= record
          end
        end

        records.values
      end

      # Parses the live HTML page. Kept as a public method so existing parse
      # tests continue to exercise it directly.
      def parse(html)
        parse_live(html)
      end

      def parse_live(html)
        doc = Nokogiri::HTML.parse(html)
        records = []
        seen = {}
        current_date = nil

        # Walk the document in order. Each table is preceded by a "Last update"
        # block containing a <b>DD.MM.YYYY</b> marker. Track the most recent
        # date we have seen and apply it to the next table.
        doc.traverse do |node|
          next unless node.element?

          if node.name == "b"
            text = node.text.strip
            current_date = Date.strptime(text, "%d.%m.%Y") if DATE_PATTERN.match?(text)
          elsif node.name == "table" && current_date
            parse_live_table(node, current_date).each do |record|
              key = [record[:date], record[:base], record[:quote]]
              # The bid/ask USD/EUR table repeats the daily fix date. Keep the
              # first (mid-rate) record per date+pair.
              next if seen[key]

              seen[key] = true
              records << record
            end
          end
        end

        records
      end

      # Parses a single per-year XLSX archive workbook from raw bytes. Each
      # monthly sheet is scanned independently so we don't depend on sheet
      # ordering or month names.
      def parse_archive(xlsx_bytes)
        records = []

        Zip::File.open_buffer(xlsx_bytes) do |zip|
          strings = shared_strings(zip)

          zip.glob("xl/worksheets/sheet*.xml").each do |entry|
            doc = Ox.load(entry.get_input_stream.read, mode: :generic, effort: :tolerant)
            sheet = doc.nodes.first
            sheet_data = sheet&.nodes&.find { |n| n.value == "sheetData" }
            rows = sheet_data&.nodes || []

            date_columns = extract_date_columns(rows, strings)
            next if date_columns.empty?

            rows.each do |row|
              label = row_label(row, strings)
              iso = label && extract_iso(label)
              next unless iso
              next if EXCLUDED_QUOTES.include?(iso)

              multiplier = UNIT_MULTIPLIERS[iso] || 1

              row.nodes.each do |cell|
                ref = cell["r"]
                next unless ref

                column = column_of(ref)
                date_column_index = date_columns[column]
                next unless date_column_index

                value = cell_numeric_value(cell)
                next unless value

                records << {
                  date: date_column_index,
                  base: iso,
                  quote: "ALL",
                  rate: value / multiplier,
                }
              end
            end
          end
        end

        records
      end

      private

      def record_key(record)
        [record[:date], record[:base], record[:quote]]
      end

      def use_live?(after:)
        return true if after.nil?

        after >= Date.today - LIVE_WINDOW_DAYS
      end

      def archive_years(after:, upto:)
        return [] unless after

        first = [after.year, ARCHIVE_MIN_YEAR].max
        last = (upto || Date.today).year
        return [] if first > last

        (first..last).to_a
      end

      def archive_urls
        @archive_urls ||= parse_archive_index(load_page(ARCHIVE_INDEX_URL))
      end

      def parse_archive_index(html)
        doc = Nokogiri::HTML.parse(html)
        urls = {}

        doc.css("a[href]").each do |link|
          href = link["href"]
          # Restricted to .xlsx — pre-2013 .xls files use the legacy BIFF
          # format that rubyzip/Ox can't open.
          next unless href&.match?(/\.xlsx\z/i)

          basename = href.split("/").last
          # File names vary year by year ("Kurs_1994_*.xls" through
          # "Kursi_i_kembimit_dhjetor_2025_*.xlsx"). The year always appears
          # as a 4-digit token surrounded by underscores in the 1990-2100
          # range. Pick the first such token to avoid matching the trailing
          # file id (e.g. the "_32487" suffix).
          year = basename.scan(/(?:^|_)((?:19|20)\d{2})_/).flatten.first
          next unless year

          year = year.to_i
          next if year < ARCHIVE_MIN_YEAR

          url = href.start_with?("http") ? href : "#{ARCHIVE_BASE_URL}#{href}"
          # Last-link-wins so the latest revision of a year (e.g. the December
          # snapshot of the current year) overrides any earlier mirror.
          urls[year] = url
        end

        urls
      end

      def load_page(url)
        body = load_binary(url)
        raise Adapter::Unavailable, "empty response body" if body.nil? || body.empty?

        body
      end

      def load_binary(url)
        uri = URI(url)
        http = Net::HTTP.new(uri.host, uri.port)
        http.use_ssl = true
        http.open_timeout = 30
        http.read_timeout = 120
        req = Net::HTTP::Get.new(uri.request_uri)
        req["User-Agent"] = USER_AGENT
        response = http.request(req)
        unless response.is_a?(Net::HTTPSuccess)
          raise Adapter::Unavailable, "unexpected response #{response.code}"
        end

        response.body
      end

      def parse_live_table(table, date)
        # Skip <thead> rows so column headers are not parsed as data.
        table.xpath(".//tr[not(ancestor::thead)]").filter_map do |row|
          parse_live_row(row, date)
        end
      end

      def parse_live_row(row, date)
        cells = row.xpath("./td|./th").map { |c| c.text.strip }
        return if cells.length < 3

        code = cells[1]
        return unless code&.match?(/\A[A-Z]{3}\z/)
        return if EXCLUDED_QUOTES.include?(code)

        value = Float(cells[2].tr(",", ""), exception: false)
        return unless value&.positive?

        multiplier = UNIT_MULTIPLIERS[code] || 1
        { date:, base: code, quote: "ALL", rate: value / multiplier }
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

      # Builds a `{ data_column_letter => date }` map by locating the header
      # row whose cells carry "DT.DD.MM.YYYY" strings (stepped every 3 columns
      # across the sheet). The rate we want sits in the column immediately to
      # the right of each date header, so we map the next column letter to the
      # parsed date.
      def extract_date_columns(rows, strings)
        rows.each do |row|
          dates_in_row = {}

          row.nodes.each do |cell|
            next unless cell["t"] == "s"

            ref = cell["r"]
            next unless ref

            value = cell.nodes.find { |n| n.value == "v" }
            next unless value

            index = value.nodes.first.to_s.to_i
            text = strings[index]
            next unless text

            match = ARCHIVE_DATE_PATTERN.match(text)
            next unless match

            date = parse_archive_date(match)
            next unless date

            data_column = next_column(column_of(ref))
            dates_in_row[data_column] = date
          end

          return dates_in_row if dates_in_row.size >= 3
        end

        {}
      end

      def parse_archive_date(match)
        Date.new(match[3].to_i, match[2].to_i, match[1].to_i)
      rescue ArgumentError
        nil
      end

      def row_label(row, strings)
        cell = row.nodes.find { |c| c["r"]&.start_with?("B") && c["t"] == "s" }
        return unless cell

        value = cell.nodes.find { |n| n.value == "v" }
        return unless value

        index = value.nodes.first.to_s.to_i
        strings[index]
      end

      def extract_iso(label)
        override = ARCHIVE_LABEL_OVERRIDES[normalize_label(label)]
        return override if override

        match = ARCHIVE_CODE_PATTERN.match(label)
        match && match[1]
      end

      def normalize_label(label)
        label.to_s.downcase.gsub(/[[:space:]]+/, " ").strip
      end

      def cell_numeric_value(cell)
        return if cell["t"] == "s"

        value = cell.nodes.find { |n| n.value == "v" }
        return unless value

        text = value.nodes.first.to_s.strip
        return if text.empty?

        rate = Float(text, exception: false)
        return unless rate&.positive?

        rate
      end

      def column_of(ref)
        ref.sub(/\d+\z/, "")
      end

      # Increments an Excel column letter (A → B, Z → AA, AZ → BA, etc.).
      def next_column(column)
        chars = column.chars
        i = chars.length - 1
        while i >= 0
          if chars[i] == "Z"
            chars[i] = "A"
            i -= 1
          else
            chars[i] = (chars[i].ord + 1).chr
            return chars.join
          end
        end

        "A#{chars.join}"
      end
    end
  end
end
