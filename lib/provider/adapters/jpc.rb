# frozen_string_literal: true

require "bigdecimal"
require "pdf-reader"
require "stringio"
require "uri"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Japan Customs publishes JPY per 1 or 100 foreign units, effective Sunday through Saturday. These weekly customs
    # observations never blend with daily reference rates. The linked PDFs form a continuous archive from January 2002.
    class JPC < Adapter
      INDEX_URL = "https://www.customs.go.jp/tetsuzuki/kawase/"
      ARCHIVE_URL = "#{INDEX_URL}list.htm".freeze
      COVERAGE_START = Date.new(2002, 1, 6)
      CURRENT_INDEX_START = Date.new(2008, 1, 1)
      PDF_HREF = /href=["']([^"']*kouji-rate(\d{8})-[^"']+\.pdf)["']/i
      NUMBER = /\A[\d,]+\.\d+\z/

      class << self
        def backfill_range = 91

        # The next Sunday's table is published during the preceding week.
        def lead_days = 7
      end

      def fetch(after: nil, upto: nil)
        start_date = [after || COVERAGE_START, COVERAGE_START].max
        end_date = [upto || (Date.today + self.class.lead_days), Date.today + self.class.lead_days].min
        return [] if start_date > end_date

        indexes = []
        indexes << ARCHIVE_URL if start_date < CURRENT_INDEX_START
        indexes << INDEX_URL if end_date >= CURRENT_INDEX_START
        reports = indexes.flat_map { |url| listing(url) }.uniq(&:first).sort_by(&:first)
          .select { |date, _| date.between?(start_date, end_date) }

        reports.flat_map.with_index do |(date, url), index|
          sleep(0.1) if index.positive?
          parse(http.get(url).to_s, date:)
        end
      end

      def parse(pdf_data, date:)
        reader = PDF::Reader.new(StringIO.new(pdf_data))
        parse_runs(reader.pages.map(&:runs), date:)
      end

      def parse_runs(pages, date:)
        # Older PDFs lack usable Japanese Unicode mappings. ISO codes and numbers remain readable, but flattened text
        # can split a row across lines. Pair them by their PDF coordinates instead of interpreting garbled headers.
        pairs = pages.flat_map do |runs|
          runs.filter_map do |code|
            next unless code.text.match?(/\A[A-Z]{3}\z/) && code.text != "ISO"

            values = runs.select do |value|
              value.x > code.x + code.width && (value.y - code.y).abs < 3 && value.text.match?(NUMBER)
            end
            raise "JPC: multiple rates for #{code.text} on #{date}" if values.size > 1

            # Some rows publish only an equivalence statement (e.g. BND equals SGD), not a numerical customs rate.
            [code.text, values.first] if values.any?
          end
        end

        # The two numeric columns are right-aligned: the left is JPY per 1 unit, the right JPY per 100. Identify their
        # right edges across all pages; layouts shift horizontally between historical editions.
        edges = pairs.map { |_, value| value.x + value.width }
        raise "JPC: missing unit columns on #{date}" if edges.empty? || edges.max - edges.min < 40

        unless edges.all? { |edge| [edge - edges.min, edges.max - edge].min < 3 }
          raise "JPC: unexpected numeric column on #{date}"
        end

        boundary = (edges.min + edges.max) / 2
        pairs.filter_map do |base, value|
          unit = value.x + value.width > boundary ? 100 : 1
          rate = BigDecimal(value.text.delete(",")) / unit
          next unless rate.positive?

          # The archive retains SUR for Russia's post-1998 ruble and YUN for the post-1994 dinar. ISO amendment 119
          # replaces YUM with CSD effective February 2003; the source eventually adopts RSD in its own right.
          base = "RUB" if base == "SUR"
          base = date < Date.new(2003, 2, 1) ? "YUM" : "CSD" if base == "YUN"
          { date:, base:, quote: "JPY", rate: }
        end
      end

      private

      def listing(url)
        links = http.get(url).to_s.scan(PDF_HREF)
        raise "JPC: no weekly PDF links on #{url}" if links.empty?

        links.map do |href, date|
          [Date.strptime(date, "%Y%m%d"), URI.join(url, href).to_s]
        end
      end
    end
  end
end
