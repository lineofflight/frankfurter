# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bsh"
require "zip"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BSh do
      before do
        VCR.insert_cassette("bsh")
      end

      after do
        VCR.eject_cassette
      end

      let(:adapter) { BSh.new }

      describe "live page" do
        it "fetches rates" do
          dataset = adapter.fetch

          _(dataset).wont_be_empty
        end

        it "emits foreign currency as base and ALL as quote" do
          records = adapter.parse_live(<<~HTML)
            <div>Last update:<b>22.05.2026</b></div>
            <TABLE>
              <TR><TD>US Dollar</TD><TD>USD</TD><td>82.24</td><td>+0.26</td></TR>
            </TABLE>
          HTML

          _(records.first[:base]).must_equal("USD")
          _(records.first[:quote]).must_equal("ALL")
          _(records.first[:rate]).must_equal(82.24)
          _(records.first[:date]).must_equal(Date.new(2026, 5, 22))
        end

        it "scales JPY from per-100 to per-1-unit" do
          records = adapter.parse_live(<<~HTML)
            <div>Last update:<b>22.05.2026</b></div>
            <TABLE>
              <TR><TD>Japanese Yen (100)</TD><TD>JPY</TD><td>51.68</td></TR>
            </TABLE>
          HTML

          _(records.first[:base]).must_equal("JPY")
          _(records.first[:rate]).must_be_close_to(0.5168, 0.0001)
        end

        it "emits XAU and XAG per troy ounce as published" do
          records = adapter.parse_live(<<~HTML)
            <div>Last update:<b>22.05.2026</b></div>
            <TABLE>
              <TR><TD>Gold(OZ 1)</TD><TD>XAU</TD><td>371473.97</td></TR>
              <TR><TD>Silver(OZ 1)</TD><TD>XAG</TD><td>6223.96</td></TR>
            </TABLE>
          HTML

          gold = records.find { |r| r[:base] == "XAU" }
          silver = records.find { |r| r[:base] == "XAG" }

          _(gold[:rate]).must_equal(371473.97)
          _(silver[:rate]).must_equal(6223.96)
        end

        it "excludes SDR" do
          records = adapter.parse_live(<<~HTML)
            <div>Last update:<b>22.05.2026</b></div>
            <TABLE>
              <TR><TD>Special Drawing Rights</TD><TD>SDR</TD><td>112.40</td></TR>
              <TR><TD>US Dollar</TD><TD>USD</TD><td>82.24</td></TR>
            </TABLE>
          HTML

          codes = records.map { |r| r[:base] }

          _(codes).wont_include("SDR")
          _(codes).must_include("USD")
        end

        it "parses multiple tables with distinct dates" do
          records = adapter.parse_live(<<~HTML)
            <div>Last update:<b>22.05.2026</b></div>
            <TABLE>
              <TR><TD>US Dollar</TD><TD>USD</TD><td>82.24</td></TR>
            </TABLE>
            <div>Last update:<b>15.05.2026</b></div>
            <TABLE>
              <TR><TD>Hungarian Forint</TD><TD>HUF</TD><td>26.47</td></TR>
            </TABLE>
          HTML

          dates = records.map { |r| r[:date] }.uniq.sort

          _(dates).must_equal([Date.new(2026, 5, 15), Date.new(2026, 5, 22)])
        end

        it "keeps the mid-rate entry when bid/ask repeats a pair on the same date" do
          records = adapter.parse_live(<<~HTML)
            <div>Last update:<b>22.05.2026</b></div>
            <TABLE>
              <TR><TD>US Dollar</TD><TD>USD</TD><td>82.24</td></TR>
            </TABLE>
            <div>Last update:<b>22.05.2026</b></div>
            <TABLE>
              <TR><TD>US Dollar</TD><TD>USD</TD><td>81.80</td><td>+0.27</td><td></td><td>82.64</td></TR>
            </TABLE>
          HTML

          usd = records.select { |r| r[:base] == "USD" }

          _(usd.size).must_equal(1)
          _(usd.first[:rate]).must_equal(82.24)
        end

        it "skips rows with non-ISO codes" do
          records = adapter.parse_live(<<~HTML)
            <div>Last update:<b>22.05.2026</b></div>
            <TABLE>
              <TR><TD>Header</TD><TD>Code</TD><td>Value</td></TR>
              <TR><TD>US Dollar</TD><TD>USD</TD><td>82.24</td></TR>
            </TABLE>
          HTML

          codes = records.map { |r| r[:base] }

          _(codes).must_equal(["USD"])
        end
      end

      describe "archive parser" do
        # Mirrors a per-year BSh workbook: one sheet, a "DT.DD.MM.YYYY" header
        # row with dates every 3 columns, currency rows whose Albanian label
        # ends in an "(ISO)" code. The rate we want sits in the column
        # immediately to the right of each date column.
        def build_workbook(dates:, rows:, header_row: 9)
          strings = []
          string_index = ->(value) {
            idx = strings.index(value)
            next idx if idx

            strings << value
            strings.length - 1
          }

          # Date columns C, F, I, L, ... (every 3rd column, starting at C).
          date_columns = dates.map.with_index { |_, i| column_letter(2 + i * 3) }
          rate_columns = date_columns.map { |c| next_letter(c) }

          header_cells = dates.each_with_index.map do |date, i|
            idx = string_index.call("    DT.#{date.strftime("%d.%m.%Y")}")
            %(<c r="#{date_columns[i]}#{header_row}" t="s"><v>#{idx}</v></c>)
          end.join

          header_xml = %(<row r="#{header_row}">#{header_cells}</row>)

          data_xml = rows.each_with_index.map do |row, row_idx|
            r = header_row + 5 + row_idx
            label_idx = string_index.call(row[:label])
            rate_cells = row[:rates].each_with_index.map do |rate, i|
              next "" if rate.nil?

              %(<c r="#{rate_columns[i]}#{r}"><v>#{rate}</v></c>)
            end.join
            %(<row r="#{r}"><c r="B#{r}" t="s"><v>#{label_idx}</v></c>#{rate_cells}</row>)
          end.join

          sheet_xml = <<~XML
            <?xml version="1.0" encoding="UTF-8" standalone="yes"?>
            <worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
              <sheetData>#{header_xml}#{data_xml}</sheetData>
            </worksheet>
          XML

          ss_items = strings.map { |s| "<si><t xml:space=\"preserve\">#{s}</t></si>" }.join
          ss_xml = <<~XML
            <?xml version="1.0" encoding="UTF-8" standalone="yes"?>
            <sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="#{strings.length}" uniqueCount="#{strings.length}">#{ss_items}</sst>
          XML

          buffer = Zip::OutputStream.write_buffer do |zip|
            zip.put_next_entry("xl/worksheets/sheet1.xml")
            zip.write(sheet_xml)
            zip.put_next_entry("xl/sharedStrings.xml")
            zip.write(ss_xml)
          end
          buffer.string
        end

        # Column letter helpers (1 → A, 2 → B, ..., 26 → Z, 27 → AA).
        def column_letter(index)
          result = +""
          loop do
            result.prepend(((index - 1) % 26 + "A".ord).chr)
            index = (index - 1) / 26
            break if index <= 0
          end
          result
        end

        def next_letter(letter)
          column_letter(letter.chars.reduce(0) { |acc, ch| acc * 26 + (ch.ord - "A".ord + 1) } + 1)
        end

        # Three dates is the minimum the parser needs to confidently identify
        # the header row, matching how real BSh sheets carry a full month of
        # business days across their column headers.
        let(:archive_dates) { [Date.new(2024, 1, 3), Date.new(2024, 1, 4), Date.new(2024, 1, 5)] }

        it "emits foreign currency as base and ALL as quote" do
          xlsx = build_workbook(
            dates: archive_dates,
            rows: [
              { label: "Dollari Amerikan (USD)", rates: [94.92, 95.10, 95.05] },
            ],
          )

          records = adapter.parse_archive(xlsx)

          _(records.first[:base]).must_equal("USD")
          _(records.first[:quote]).must_equal("ALL")
          _(records.first[:rate]).must_equal(94.92)
          _(records.first[:date]).must_equal(Date.new(2024, 1, 3))
        end

        it "scales JPY from per-100 to per-1-unit" do
          xlsx = build_workbook(
            dates: archive_dates,
            rows: [
              { label: "Jeni Japonez (per 100) (JPY)", rates: [66.15, 66.20, 66.10] },
            ],
          )

          records = adapter.parse_archive(xlsx)

          _(records.first[:base]).must_equal("JPY")
          _(records.first[:rate]).must_be_close_to(0.6615, 0.0001)
        end

        it "emits XAU and XAG per troy ounce as published" do
          xlsx = build_workbook(
            dates: archive_dates,
            rows: [
              { label: "Ari (oz) (XAU)", rates: [193869.19, 194238.73, 195636.03] },
              { label: "Argjendi (oz) (XAG)", rates: [2208.02, 2183.43, 2202.58] },
            ],
          )

          records = adapter.parse_archive(xlsx)
          gold = records.find { |r| r[:base] == "XAU" }
          silver = records.find { |r| r[:base] == "XAG" }

          _(gold[:rate]).must_equal(193869.19)
          _(silver[:rate]).must_equal(2208.02)
        end

        it "excludes SDR" do
          xlsx = build_workbook(
            dates: archive_dates,
            rows: [
              { label: "Spec. Drawing RIGHTS (SDR)", rates: [123.25, 122.94, 124.02] },
              { label: "Dollari Amerikan (USD)", rates: [94.92, 95.10, 95.05] },
            ],
          )

          records = adapter.parse_archive(xlsx)
          codes = records.map { |r| r[:base] }

          _(codes).wont_include("SDR")
          _(codes).must_include("USD")
        end

        it "parses multiple date columns per row" do
          xlsx = build_workbook(
            dates: [Date.new(2024, 1, 3), Date.new(2024, 1, 4), Date.new(2024, 1, 5)],
            rows: [
              { label: "Dollari Amerikan (USD)", rates: [94.92, 95.10, 95.05] },
            ],
          )

          records = adapter.parse_archive(xlsx)

          _(records.map { |r| r[:date] }).must_equal([
            Date.new(2024, 1, 3),
            Date.new(2024, 1, 4),
            Date.new(2024, 1, 5),
          ])
          _(records.map { |r| r[:rate] }).must_equal([94.92, 95.10, 95.05])
        end

        it "skips empty cells" do
          xlsx = build_workbook(
            dates: [Date.new(2024, 1, 3), Date.new(2024, 1, 4), Date.new(2024, 1, 5)],
            rows: [
              { label: "Dollari Amerikan (USD)", rates: [94.92, nil, 95.05] },
            ],
          )

          records = adapter.parse_archive(xlsx)

          _(records.map { |r| r[:date] }).must_equal([
            Date.new(2024, 1, 3),
            Date.new(2024, 1, 5),
          ])
        end

        it "skips rows whose label carries no ISO code" do
          xlsx = build_workbook(
            dates: archive_dates,
            rows: [
              { label: "Monedhat e huaja", rates: [0, 0, 0] },
              { label: "Dollari Amerikan (USD)", rates: [94.92, 95.10, 95.05] },
            ],
          )

          records = adapter.parse_archive(xlsx)
          codes = records.map { |r| r[:base] }.uniq

          _(codes).must_equal(["USD"])
        end
      end

      describe "archive fetch" do
        before do
          VCR.eject_cassette
          VCR.insert_cassette("bsh_archive")
        end

        after do
          VCR.eject_cassette
          VCR.insert_cassette("bsh")
        end

        it "ingests a historical date range from the per-year XLSX archive" do
          dataset = adapter.fetch(after: Date.new(2024, 1, 1), upto: Date.new(2024, 1, 10))

          _(dataset).wont_be_empty
          _(dataset.map { |r| r[:quote] }.uniq).must_equal(["ALL"])
          _(dataset.map { |r| r[:date] }.min).must_be(:>=, Date.new(2024, 1, 1))
          _(dataset.map { |r| r[:date] }.max).must_be(:<=, Date.new(2024, 1, 10))
          _(dataset.map { |r| r[:base] }).must_include("USD")
          _(dataset.map { |r| r[:base] }).must_include("EUR")
        end
      end

      describe "archive index" do
        it "extracts per-year XLSX URLs and ignores legacy .xls files" do
          html = <<~HTML
            <html><body>
              <a href="/rc/doc/Kurs_1994_10980.xls">1994</a>
              <a href="/rc/doc/kursi_2013_10999.xlsx">2013</a>
              <a href="/rc/doc/Kursi_i_kembimit_2024_29746.xlsx">2024</a>
              <a href="/rc/doc/Kursi_i_kembimit_dhjetor_2025_32487.xlsx">2025</a>
              <a href="/some/other/page.html">unrelated</a>
            </body></html>
          HTML

          urls = adapter.send(:parse_archive_index, html)

          _(urls.keys.sort).must_equal([2013, 2024, 2025])
          _(urls[2013]).must_equal("https://www.bankofalbania.org/rc/doc/kursi_2013_10999.xlsx")
          _(urls[2025]).must_equal("https://www.bankofalbania.org/rc/doc/Kursi_i_kembimit_dhjetor_2025_32487.xlsx")
        end

        it "skips files outside the .xlsx era" do
          html = <<~HTML
            <html><body>
              <a href="/rc/doc/kursi_2012_10998.xls">2012</a>
              <a href="/rc/doc/kursi_2013_10999.xlsx">2013</a>
            </body></html>
          HTML

          urls = adapter.send(:parse_archive_index, html)

          _(urls.keys).must_equal([2013])
        end
      end
    end
  end
end
