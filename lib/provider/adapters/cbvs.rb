# frozen_string_literal: true

require "date"
require "pdf-reader"
require "stringio"
require "uri"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Centrale Bank van Suriname — indicative quotes in SRD for USD, EUR, GBP, CNY and seven Caribbean and South
    # American currencies, published as PDF notices ("wisselkoersnoteringen"). Each notice carries buy and sell columns
    # for transfers (wissels, cheques en overmakingen) and for banknotes; we take the midpoint of the transfer pair. GYD
    # is quoted per 100 and is normalised to a per-unit rate.
    #
    # The archive page lists one PDF per year for 2009-2023, one per month from 2024 and one per fixing for the current
    # month (three a day, at 10:00, 12:30 and 15:00 local). Fetch scrapes the page, classifies each link by the span it
    # covers and downloads only those overlapping the requested window. Yearly PDFs are large (up to 10 MB, 1500 pages)
    # and slow to parse, so history is fetched in a single pass rather than in chunks that would redownload the same
    # file.
    #
    # One page per fixing, in the same text layout since 2009: a Dutch date line ("08 SEPTEMBER 2026", from 2021 with
    # "VASTGESTELD OMSTREEKS 15:00U"), then one row per currency with the ISO code in parentheses and four numbers.
    # Pages without a Dutch date line (gold-certificate valuations, an occasional English rendition of a notice, the
    # sell-only extended overview appended to the 2022 file) are skipped. 2013 uses a dot as the decimal separator;
    # every other year uses a comma.
    #
    # A day with several fixings yields the last one. For the current day that means waiting for the closing fixing:
    # storing the 10:00 quote would freeze it, since backfill never rewrites a stored row. In March 2021 the notice
    # appended USD and EUR quotes at the maximum selling rate below the main table; the main table comes first and wins.
    #
    # Direction: foreign currency in base, SRD in quote (1 USD = X SRD), like other pivot-in-quote adapters (NBG, BBK).
    class CBVS < Adapter
      HOST = "https://www.cbvs.sr"
      ARCHIVE_URL = "#{HOST}/statistieken/financiele-markten-statistieken/dagelijkse-publicaties".freeze
      PDF_HREF = %r{href="([^"]*/Wisselkoersen/[^"]*\.pdf)"}i
      MONTHS = ["JANUARI", "FEBRUARI", "MAART", "APRIL", "MEI", "JUNI", "JULI", "AUGUSTUS", "SEPTEMBER", "OKTOBER",
                "NOVEMBER", "DECEMBER",].freeze
      MONTH_PATTERN = MONTHS.join("|").freeze

      # Filename shapes on the archive page, one per generation. Daily: "DO260908 15.00 uur.pdf" (yymmdd, then the
      # fixing time; "uu" typos occur). Monthly: "WK_JANUARI_2024.pdf" or "WisselkoersnoteringMaarti2025.pdf" (typos
      # occur). Yearly: "Jaar_2010.pdf", "Jaar2014.pdf", "jaar-2009sep-dec.pdf", "Jaar_2023_WK.pdf".
      DAILY_FILE = /\ADO(\d{2})(\d{2})(\d{2})\b/
      MONTHLY_FILE = /(#{MONTH_PATTERN})[A-Z]*?_?(\d{4})\.pdf\z/i
      YEARLY_FILE = /JAAR[_ -]?(\d{4})/i

      HEADER = /(\d{1,2})\s+(#{MONTH_PATTERN})\s+(\d{4})(?:\s+VASTGESTELD\s+OMSTREEKS\s+(\d{1,2})[.:](\d{2}))?/
      NUMBER = /\d[\d.,]*/
      # "U.S. DOLLAR (USD)", "GUYANA DOLLAR (PER 100 GYD)", "GUYANA DOLLAR (GYD PER 100 )", "CHINESE YUAN RENMINBI (PER
      # CNY)". A few rows lose the closing parenthesis to the text extraction.
      ROW = /\A[A-Z][A-Z .&]*?\s*\((?:PER\s+)?(?:(\d+)\s+)?([A-Z]{3})(?:\s+PER\s+(\d+))?\s*\)?\s+
        (#{NUMBER})\s+(#{NUMBER})\s+(#{NUMBER})\s+(#{NUMBER})\z/x
      CLOSING_FIXING = "15:00"
      TITLE = "WISSELKOERSNOTERINGEN"

      def fetch(after: nil, upto: nil)
        upto ||= Date.today
        fixings = documents(after, upto).each_with_index.flat_map do |url, i|
          sleep(1) unless i.zero?
          parse(http.get(url).to_s)
        end

        closing(fixings, after, upto).flat_map { |fixing| fixing[:records] }
      end

      # The last fixing per date within the window, in the order given for ties. Today's date is held back until its
      # closing fixing is out.
      def closing(fixings, after, upto, today: Date.today)
        fixings.each_with_object({}) do |fixing, latest|
          date = fixing[:date]
          next if after && date < after
          next if date > upto
          next if date >= today && fixing[:time] && fixing[:time] < CLOSING_FIXING

          current = latest[date]
          latest[date] = fixing unless current && current[:time].to_s > fixing[:time].to_s
        end.values
      end

      # One fixing per notice page: { date:, time:, records: }. Time is "HH:MM" from 2021 and nil before.
      def parse(pdf_data)
        reader = PDF::Reader.new(StringIO.new(pdf_data))
        reader.pages.filter_map do |page|
          parse_page(page.text)
        rescue PDF::Reader::MalformedPDFError
          # One page of the June 2025 monthly file has an invalid font. The archive is static, so raising would block
          # the provider at that file for good; the day keeps its other fixings.
          nil
        end
      end

      def parse_page(text)
        return unless text.include?(TITLE)

        header = text.match(HEADER)
        return unless header

        date = Date.new(header[3].to_i, MONTHS.index(header[2]) + 1, header[1].to_i)
        time = "#{header[4].rjust(2, "0")}:#{header[5]}" if header[4]
        records = text.each_line.filter_map { |line| record(line.strip, date) }.uniq { |record| record[:base] }
        return if records.empty?

        { date:, time:, records: }
      end

      # The span of dates a listed PDF covers, or nil for a link that is not a rate notice.
      def coverage(href)
        name = File.basename(href)
        if (match = name.match(DAILY_FILE))
          date = Date.new(2000 + match[1].to_i, match[2].to_i, match[3].to_i)
          date..date
        elsif (match = name.match(MONTHLY_FILE))
          first = Date.new(match[2].to_i, MONTHS.index(match[1].upcase) + 1, 1)
          first..(first.next_month - 1)
        elsif (match = name.match(YEARLY_FILE))
          year = match[1].to_i
          Date.new(year, 1, 1)..Date.new(year, 12, 31)
        end
      end

      private

      def record(line, date)
        match = line.match(ROW)
        return unless match

        unit = (match[1] || match[3] || 1).to_i
        return if unit.zero?

        rate = midpoint(number(match[4]), number(match[5]))
        rate = (BigDecimal(rate.to_s) / unit).to_f unless unit == 1
        return if rate.zero?

        { date:, base: match[2], quote: "SRD", rate:,
          **prices(bid: number(match[4]), ask: number(match[5]), unit:), }
      end

      # Dutch notation ("1.073,45") from 2014; a bare dot ("3.250") is the decimal separator in 2013.
      def number(text)
        text = text.delete(".") if text.include?(",")
        Float(text.tr(",", "."))
      end

      # Archive links overlapping the window, oldest first. Of a day's several fixing PDFs only the latest is fetched.
      def documents(after, upto)
        body = http.get(ARCHIVE_URL).to_s
        hrefs = body.scan(PDF_HREF).flatten.map { |href| href.gsub("%20", " ").delete("\t") }.uniq
        raise "no rate PDFs on #{ARCHIVE_URL}" if hrefs.empty?

        listed = hrefs.filter_map do |href|
          span = coverage(href)
          next unless span
          next if after && span.end < after
          next if span.begin > upto

          [span, href]
        end

        daily, bulk = listed.partition { |span, _| span.begin == span.end }
        daily = daily.group_by(&:first).values.map { |entries| entries.max_by { |_, href| File.basename(href) } }

        (bulk + daily).sort_by { |span, _| span.begin }.map do |_, href|
          URI.join(HOST, URI::RFC2396_PARSER.escape(href)).to_s
        end
      end
    end
  end
end
