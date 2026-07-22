# frozen_string_literal: true

require "ox"
require "zip"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Central Bank of Egypt. Publishes daily buy/sell rates against EGP for 18 currencies via an XLSX
    # download from the historical-data page. The page requires a session cookie + anti-forgery token
    # scraped from the HTML form; both are reused across batches within a single backfill.
    #
    # The XLSX response has four columns: Date (Excel serial), Currency (full English name),
    # Buy, Sell. We coerce buy/sell to mid = (buy + sell) / 2 (issue #314 pattern). JPY is quoted
    # per 100 units in CBE data ("Japanese Yen 100" option) and is divided by 100.
    #
    # CBE publishes "1 foreign = X EGP" (e.g. 1 USD = 52 EGP), so foreign is the base and EGP the
    # quote. Matches the pivot-in-quote convention used by NBG and BBK.
    #
    # The site sits behind an F5 BIG-IP ASM WAF; we keep backfill_range small (30 days) and pause
    # one second between batches to stay under the rate-limit threshold.
    #
    # Attribution required: per CBE disclaimer, the Central Bank of Egypt must be cited as the
    # source when information is distributed or reproduced.
    class CBE < Adapter
      HISTORICAL_URL = "https://www.cbe.org.eg/en/economic-research/statistics/cbe-exchange-rates/historical-data"
      API_URL = "https://www.cbe.org.eg/api/statistics/GetHistoricalData"
      DATA_SOURCE_ID = "19CFFDDBFF494350A5E9C6A4397FC7DF"
      FALLBACK_URL = "/en/economic-research/statistics/cbe-exchange-rates/historical-data"
      USER_AGENT = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " \
        "AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36"
      TOKEN_PATTERN = /name="__RequestVerificationToken"[^>]*value="([^"]+)"/

      # CBE returns rates per currency name; map to ISO codes. JPY is quoted per 100 units.
      CURRENCIES = {
        "US Dollar" => ["USD", 1],
        "Euro" => ["EUR", 1],
        "Pound Sterling" => ["GBP", 1],
        "Canadian Dollar" => ["CAD", 1],
        "Danish Krone" => ["DKK", 1],
        "Norwegian Krone" => ["NOK", 1],
        "Swedish Krona" => ["SEK", 1],
        "Swiss Franc" => ["CHF", 1],
        "Japanese Yen 100" => ["JPY", 100],
        "Saudi Riyal" => ["SAR", 1],
        "Kuwaiti Dinar" => ["KWD", 1],
        "UAE Dirham" => ["AED", 1],
        "Australian Dollar" => ["AUD", 1],
        "Bahraini Dinar" => ["BHD", 1],
        "Omani Riyal" => ["OMR", 1],
        "Qatari Riyal" => ["QAR", 1],
        "Jordanian Dinar" => ["JOD", 1],
        "Chinese Yuan" => ["CNY", 1],
      }.freeze

      class << self
        def backfill_range = 30

        # Override the base fetch_each to reuse a single adapter instance across
        # batches. The default implementation calls `new.fetch` per chunk, which
        # would throw away the WAF session cookie/token between batches and
        # would also defeat the inter-batch pacing below.
        #
        # No transient-error rescue here: the base client already retries transport errors internally (retriable, 5
        # tries). Anything else (e.g. an HTTP::StatusError from the WAF) propagates so Provider#backfill logs and skips
        # it; the next tick resumes from last_synced.
        def fetch_each(after: nil)
          return if after && after >= Date.today

          adapter = new
          loop do
            upto = after + backfill_range - 1 if after && backfill_range
            upto = nil if upto && upto >= Date.today
            records = adapter.fetch(after:, upto:)
            yield records if records.any?
            break unless upto

            after = upto + 1
          end
        end
      end

      def fetch(after: nil, upto: nil)
        start_date = after || Date.new(2024, 3, 1)
        end_date = upto || Date.today
        # Window sanity: nothing to fetch when the chunk starts after it ends.
        return [] if start_date > end_date

        sleep(1) if @session
        ensure_session
        @session = true
        xlsx = post_historical(start_date, end_date)

        parse(xlsx)
      end

      def parse(xlsx)
        rows = read_xlsx(xlsx)
        raise "CBE: no data rows in historical-data XLSX" if rows.empty?

        rows.filter_map { |row| parse_row(row) }
      end

      private

      def parse_row(row)
        currency_name = row[:currency]
        iso, units = CURRENCIES[currency_name]
        return unless iso

        date = excel_serial_to_date(row[:date])
        return unless date

        buy = row[:buy]
        sell = row[:sell]
        return if buy.nil? || sell.nil?

        mid = (buy + sell) / 2.0
        return if mid.zero?

        rate = mid / units

        { date:, base: iso, quote: "EGP", rate: }
      end

      def excel_serial_to_date(serial)
        return unless serial.is_a?(Numeric)

        # Excel epoch is 1899-12-30 (off-by-one for the fictional 1900-02-29).
        Date.new(1899, 12, 30) + serial.to_i
      end

      def ensure_session
        return if @token && @cookie

        response = http.headers("User-Agent" => USER_AGENT).get(HISTORICAL_URL)

        match = response.to_s.match(TOKEN_PATTERN)
        raise "CBE: token not found on historical-data page" unless match

        @token = match[1]
        @cookie = extract_cookies(response)
      end

      def post_historical(start_date, end_date)
        pairs = [
          ["__RequestVerificationToken", @token],
          ["DataSourceId", DATA_SOURCE_ID],
          ["FallbackUrl", FALLBACK_URL],
          ["LanguageName", "en"],
          ["FromDateRaw", start_date.strftime("%d/%m/%Y")],
          ["ToDateRaw", end_date.strftime("%d/%m/%Y")],
          *CURRENCIES.keys.map { |name| ["SelectedSelectOptions", name] },
          ["SubmitAction", "2"],
        ]

        http
          .headers(
            "User-Agent" => USER_AGENT,
            "Content-Type" => "application/x-www-form-urlencoded",
            "Cookie" => @cookie,
            "Referer" => HISTORICAL_URL,
            "Origin" => "https://www.cbe.org.eg",
          )
          .post(API_URL, body: URI.encode_www_form(pairs))
          .to_s
      end

      def extract_cookies(response)
        response.headers.get("Set-Cookie").map { |c| c.split(";").first }.join("; ")
      end

      def read_xlsx(body)
        sheet_xml = nil
        shared_strings_xml = nil

        Zip::File.open_buffer(body) do |zip|
          zip.each do |entry|
            case entry.name
            when "xl/worksheets/sheet1.xml"
              sheet_xml = entry.get_input_stream.read
            when "xl/sharedStrings.xml"
              shared_strings_xml = entry.get_input_stream.read
            end
          end
        end
        raise "CBE: sheet1.xml missing from XLSX export" unless sheet_xml

        shared_strings = parse_shared_strings(shared_strings_xml)
        parse_sheet(sheet_xml, shared_strings)
      end

      def parse_shared_strings(xml)
        raise "CBE: sharedStrings.xml missing from XLSX export" unless xml

        doc = Ox.parse(xml)
        root = doc.is_a?(Ox::Document) ? doc.nodes.find { |n| n.is_a?(Ox::Element) } : doc
        raise "CBE: malformed sharedStrings.xml in XLSX export" unless root

        root.locate("si").map do |si|
          texts = si.locate("t").map { |t| t.text.to_s }
          texts.join
        end
      end

      def parse_sheet(xml, shared_strings)
        doc = Ox.parse(xml)
        root = doc.is_a?(Ox::Document) ? doc.nodes.find { |n| n.is_a?(Ox::Element) } : doc
        raise "CBE: malformed worksheet XML in XLSX export" unless root

        rows = []

        root.locate("sheetData/row").each do |row_el|
          cells = row_el.locate("c")
          next if cells.size < 4

          date_cell = cells[0]
          currency_cell = cells[1]
          buy_cell = cells[2]
          sell_cell = cells[3]
          date = cell_value(date_cell, shared_strings)
          next unless date.is_a?(Numeric)

          currency = cell_value(currency_cell, shared_strings)
          next unless currency.is_a?(String)

          buy = cell_value(buy_cell, shared_strings)
          sell = cell_value(sell_cell, shared_strings)

          rows << {
            date: date,
            currency: currency,
            buy: buy.is_a?(Numeric) ? buy : nil,
            sell: sell.is_a?(Numeric) ? sell : nil,
          }
        end

        rows
      end

      def cell_value(cell, shared_strings)
        return unless cell

        type = cell["t"]
        value_el = cell.locate("v").first
        return unless value_el

        raw = value_el.text.to_s

        case type
        when "s"
          shared_strings[Integer(raw)]
        when "str", "inlineStr"
          raw
        else
          Float(raw)
        end
      end
    end
  end
end
