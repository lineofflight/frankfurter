# frozen_string_literal: true

require "date"
require "spreadsheet"
require "stringio"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banque du Liban.
    #
    # Daily official exchange rates of the Lebanese pound (LBP) against seven currencies (USD, EUR, GBP, JPY, CHF, AUD,
    # CAD), published as a single rolling XLS with one sheet per calendar year (current year and the two before it).
    # Each sheet has columns Period | Currency | Bid | Ask | Mid, newest row first, with Period as an Excel date cell.
    # Rows before the 2024-03-28 step to 89,500 carry a real bid/ask spread; since then bid, ask and mid coincide. We
    # emit the published Mid directly in both regimes.
    #
    # Rates are published as "1 foreign = X LBP": foreign currency is the base, LBP is the quote, matching NBG/BBK.
    #
    # The workbook is the only archive: years drop off the file as it rolls forward, so anything older than the window
    # must already be stored. The site's HTML pages sit behind a Cloudflare JS challenge, but the XLS itself is served
    # plainly.
    class BDL < Adapter
      DATA_URL = "https://www.bdl.gov.lb/CB%20Com/Statistics%20And%20Research/Daily/BDL_DailyExchangeRates.xls"

      class << self
        # Whole-archive fetch: a single XLS holds every year on offer. Large range keeps it in one fetch.
        def backfill_range = 36_525
      end

      def fetch(after: nil, upto: nil)
        parse(download(DATA_URL), after:, upto:)
      end

      def parse(xls_data, after: nil, upto: nil)
        book = Spreadsheet.open(StringIO.new(xls_data.to_s))
        raise "BDL: workbook has no worksheets" if book.worksheets.empty?

        records = book.worksheets.flat_map { |sheet| parse_sheet(sheet, after:, upto:) }

        # The file occasionally repeats a day verbatim (2025-11-17 appears twice in the 2025 sheet).
        records.uniq { |r| [r[:date], r[:base]] }
      end

      private

      def download(url)
        http.get(url).to_s
      end

      def parse_sheet(sheet, after:, upto:)
        records = []

        sheet.each do |row|
          date = parse_date(row[0])
          next unless date
          next if after && date < after
          next if upto && date > upto

          base = row[1].to_s.strip.upcase
          next unless base.match?(/\A[A-Z]{3}\z/)

          rate = row[4]
          next unless rate.is_a?(Numeric) && rate.positive?

          records << { date:, base:, quote: "LBP", rate: }
        end

        records
      end

      def parse_date(cell)
        case cell
        when DateTime then cell.to_date
        when Date then cell
        end
      end
    end
  end
end
