# frozen_string_literal: true

require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Bank of Albania (Banka e Shqipërisë).
    # Scrapes the official exchange-rate page and returns the latest published
    # rates against the Albanian lek (ALL).
    #
    # The page renders two tables:
    #   1. Daily fix (~16 currencies, updated each business day around 12:30 Tirane).
    #   2. Weekly minor-currency fix (HUF, RUB, CZK, MKD), updated each Thursday.
    # Each table carries its own "Last update" date. We emit records at the
    # provider-published date.
    #
    # Rates follow the "1 unit foreign = X ALL" convention, matching the NBG and
    # BBK pivot-in-quote orientation. JPY is published per 100 units in the
    # source and divided down to a per-1-unit rate. XAU/XAG are published per
    # troy ounce, which matches the in-app convention, so no conversion.
    # SDR is dropped because it is a composite unit; Money does not register it.
    #
    # The latest endpoint only carries the current day. Historical data lives in
    # per-year XLSX archives that vary in naming and internal layout across the
    # 1994-present range and is not yet ingested — see issue #371 follow-up.
    class BSh < Adapter
      URL = "https://www.bankofalbania.org/Markets/Official_exchange_rate/"
      USER_AGENT = "Mozilla/5.0 (compatible; Frankfurter/2.0; +https://frankfurter.dev)"

      # Each table block on the page begins with a "Last update: DD.MM.YYYY"
      # marker followed by a <TABLE> element.
      TABLE_BLOCK = %r{Last update:.*?<b>(\d{2}\.\d{2}\.\d{4})</b>.*?<TABLE\b(.*?)</TABLE>}mi
      ROW = %r{<TR\b[^>]*>(.*?)</TR>}mi
      CELL = %r{<TD\b[^>]*>(.*?)</TD>}mi

      # Quotes published per N units in the source. Divide by N to normalise to
      # a per-1-unit rate.
      UNIT_MULTIPLIERS = {
        "JPY" => 100,
      }.freeze

      # SDR is a composite unit and not an ISO 4217 currency we surface.
      EXCLUDED_QUOTES = ["SDR"].freeze

      def fetch(after: nil, upto: nil)
        records = parse(load_page)
        records = records.select { |r| r[:date] >= after } if after
        records = records.select { |r| r[:date] <= upto } if upto
        records
      end

      def parse(html)
        records = []
        seen = {}

        html.scan(TABLE_BLOCK) do |date_str, table_body|
          date = Date.strptime(date_str, "%d.%m.%Y")
          parse_table(table_body, date).each do |record|
            key = [record[:date], record[:base], record[:quote]]
            # The bid/ask USD/EUR table repeats the daily fix date. Keep the
            # first (mid-rate) record per date+pair.
            next if seen[key]

            seen[key] = true
            records << record
          end
        end

        records
      end

      private

      def load_page
        uri = URI(URL)
        http = Net::HTTP.new(uri.host, uri.port)
        http.use_ssl = true
        http.open_timeout = 30
        http.read_timeout = 60
        req = Net::HTTP::Get.new(uri)
        req["User-Agent"] = USER_AGENT
        http.request(req).body
      end

      def parse_table(body, date)
        body.scan(ROW).filter_map do |row_html,|
          parse_row(row_html, date)
        end
      end

      def parse_row(row_html, date)
        cells = row_html.scan(CELL).flatten.map { |c| strip_html(c) }
        return if cells.length < 3

        code = cells[1]
        return unless code&.match?(/\A[A-Z]{3}\z/)
        return if EXCLUDED_QUOTES.include?(code)

        value = Float(cells[2].tr(",", ""), exception: false)
        return unless value&.positive?

        multiplier = UNIT_MULTIPLIERS[code] || 1
        { date:, base: code, quote: "ALL", rate: value / multiplier }
      end

      def strip_html(str)
        str.gsub(/<[^>]+>/, "").gsub("&nbsp;", " ").strip
      end
    end
  end
end
