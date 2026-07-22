# frozen_string_literal: true

require "ox"
require "zip"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # State Bank of Pakistan. Publishes the "Daily Average Banks' Floating Exchange Rates"
    # series as two XLSX workbooks — a rolling current-month file and a historical archive
    # going back to 2013-07-02. Both files share the same wide layout: one row per foreign
    # currency, one column per date, with cell values expressed as "Pak Rupees per Currency
    # Unit" (e.g. 1 USD = 279.35 PKR).
    #
    # Because PKR sits in the denominator of every quoted rate, the source's native
    # direction is "foreign currency as base, PKR as quote". Records are emitted with that
    # same orientation — matching the pivot-in-quote pattern used by NBG, BBK, and friends.
    # The blender handles rebase to a common pivot.
    #
    # SBP publishes finalized monthly snapshots roughly three weeks after the month ends,
    # so the most recent observation typically trails other providers by up to a month.
    #
    # Cloudflare blocks plain-curl requests; a browser User-Agent header is required.
    # The site footer reads "Copyright (c) 2020. All Rights Reserved." There is no
    # explicit terms-of-use page (sbp.org.pk/disclaim.htm and /about/copyright.htm 404).
    class SBP < Adapter
      CURRENT_URL = "https://www.sbp.org.pk/ecodata/BFER_Daily.xlsx"
      ARCHIVE_URL = "https://www.sbp.org.pk/ecodata/BFER_Daily_Arch.xlsx"

      # Excel stores dates as the number of days since this epoch (with the 1900-leap-year
      # quirk baked into the offset — 1899-12-30 sidesteps it for dates after 1900-03-01).
      EXCEL_EPOCH = Date.new(1899, 12, 30)

      # Map the source's currency-name labels (column B in each currency row) to ISO codes.
      # Two label variants per currency are common between the current and archive workbooks
      # ("Singaporian Dollar" vs "Singapore Dollar", "Japnese Yen" vs "Japanese Yen", etc.).
      # We normalize whitespace/punctuation/case before lookup, so register both spellings.
      CURRENCIES = {
        "australian dollar" => "AUD",
        "bahraini dinar" => "BHD",
        "canadian dollar" => "CAD",
        "chinese yuan" => "CNY",
        "danish krone" => "DKK",
        "euro" => "EUR",
        "hong kong dollar" => "HKD",
        "hongkong dollar" => "HKD",
        "japanese yen" => "JPY",
        "japnese yen" => "JPY",
        "kuwaiti dinar" => "KWD",
        "malaysian ringgit" => "MYR",
        "new zealand dollar" => "NZD",
        "norwegian krone" => "NOK",
        "omani riyal" => "OMR",
        "qatari riyal" => "QAR",
        "saudi arabian riyal" => "SAR",
        "singapore dollar" => "SGD",
        "singaporian dollar" => "SGD",
        "swedish krona" => "SEK",
        "swedish krone" => "SEK",
        "swiss franc" => "CHF",
        "thai baht" => "THB",
        "thai bhat" => "THB",
        "turkish lira" => "TRY",
        "uae dirham" => "AED",
        "u.a.e. dirham" => "AED",
        "uk pound sterling" => "GBP",
        "u.k. pound sterling" => "GBP",
        "u.s. dollar" => "USD",
      }.freeze

      def fetch(after: nil, upto: nil)
        records = {}

        # Archive first, current second — current entries overwrite archive on overlap
        # so any same-day revisions in the rolling file win the dedupe.
        [ARCHIVE_URL, CURRENT_URL].each do |url|
          parse(download(url)).each do |record|
            next if after && record[:date] < after
            next if upto && record[:date] > upto

            key = [record[:date], record[:base]]
            records[key] = record
          end
        end

        records.values
      end

      def parse(xlsx_bytes)
        records = []

        Zip::File.open_buffer(xlsx_bytes) do |zip|
          strings = shared_strings(zip)
          sheet_xml = zip.find_entry("xl/worksheets/sheet1.xml").get_input_stream.read
          doc = Ox.load(sheet_xml, mode: :generic, effort: :tolerant)
          sheet_data = doc.nodes.first.nodes.find { |n| n.value == "sheetData" }
          rows = sheet_data&.nodes || []

          date_map = extract_date_map(rows)

          rows.each do |row|
            label = row_currency_label(row, strings)
            iso = label && CURRENCIES[normalize_label(label)]
            next unless iso

            row.nodes.each do |cell|
              ref = cell["r"]
              next unless ref

              column = ref.sub(/\d+\z/, "")
              date = date_map[column]
              next unless date

              value = cell_numeric_value(cell)
              next unless value

              records << { date:, base: iso, quote: "PKR", rate: value }
            end
          end
        end

        records
      end

      private

      def download(url)
        http.get(url).to_s
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

      # The date header row is identified structurally: it contains many inline numeric
      # cells whose value is a plausible Excel serial date (post-1980, pre-2100). This
      # lets us cope with SBP shifting metadata rows around between workbook revisions
      # without hardcoding row numbers.
      def extract_date_map(rows)
        rows.each do |row|
          serials = row.nodes.filter_map do |cell|
            next nil if cell["t"] == "s"

            value = cell.nodes.find { |n| n.value == "v" }
            next nil unless value

            number = value.nodes.first.to_s.to_f
            number if number > 30_000 && number < 80_000
          end

          next if serials.size < 3

          map = {}
          row.nodes.each do |cell|
            next if cell["t"] == "s"

            ref = cell["r"]
            next unless ref

            value = cell.nodes.find { |n| n.value == "v" }
            next unless value

            number = value.nodes.first.to_s
            next if number.empty?

            float = number.to_f
            next if float <= 30_000 || float >= 80_000

            map[ref.sub(/\d+\z/, "")] = EXCEL_EPOCH + number.to_i
          end
          return map
        end

        {}
      end

      def row_currency_label(row, strings)
        cell = row.nodes.find { |c| c["r"]&.start_with?("B") && c["t"] == "s" }
        return unless cell

        value = cell.nodes.find { |n| n.value == "v" }
        return unless value

        index = value.nodes.first.to_s.to_i
        strings[index]
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

      def normalize_label(label)
        label.to_s.downcase.gsub(/[[:space:]]+/, " ").strip
      end
    end
  end
end
