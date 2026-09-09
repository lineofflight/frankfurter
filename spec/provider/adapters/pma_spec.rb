# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/pma"
require "zip"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe PMA do
      before do
        # The POST body carries a per-session anti-forgery token, so match on method and URI only.
        VCR.insert_cassette("pma", match_requests_on: [:method, :uri], allow_playback_repeats: true)
      end

      after { VCR.eject_cassette }

      let(:adapter) { PMA.new }

      it "fetches rates for a date range" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 1), upto: Date.new(2026, 9, 8))
        dates = dataset.map { |r| r[:date] }.uniq.sort

        _(dates.min).must_equal(Date.new(2026, 9, 1))
        _(dates.max).must_equal(Date.new(2026, 9, 8))
        _(dates).wont_include(Date.new(2026, 9, 4)) # Friday
        _(dates).wont_include(Date.new(2026, 9, 5)) # Saturday
      end

      it "fetches all 25 pairs per date" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 1), upto: Date.new(2026, 9, 8))
        sample = dataset.select { |r| r[:date] == Date.new(2026, 9, 8) }

        _(sample.size).must_equal(25)
        _(sample.map { |r| [r[:base], r[:quote]] }).must_include(["USD", "ILS"])
        _(sample.map { |r| [r[:base], r[:quote]] }).must_include(["EUR", "USD"])
        _(sample.map { |r| [r[:base], r[:quote]] }).must_include(["JOD", "ILS"])
      end

      it "returns USD/ILS in a plausible range" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 1), upto: Date.new(2026, 9, 8))
        usd_ils = dataset.find { |r| r[:date] == Date.new(2026, 9, 8) && r[:base] == "USD" && r[:quote] == "ILS" }

        _(usd_ils[:rate]).must_be(:>, 2.5)
        _(usd_ils[:rate]).must_be(:<, 4.5)
      end

      it "returns nothing when the window is inverted" do
        _(adapter.fetch(after: Date.new(2026, 9, 8), upto: Date.new(2026, 9, 1))).must_be_empty
      end

      describe "#parse" do
        it "reads the published mid, not a synthesized one" do
          records = adapter.parse(workbook([["2026/09/01", "USD/JOD", "0.7080", "0.7100", "0.709"]]))

          _(records).must_equal([{ date: Date.new(2026, 9, 1), base: "USD", quote: "JOD", rate: 0.709 }])
        end

        it "keeps each pair in its published direction" do
          records = adapter.parse(workbook([
            ["2026/09/01", "USD/ILS", "2.9903", "2.9944", "2.9924"],
            ["2026/09/01", "GBP/USD", "1.3546", "1.3547", "1.3547"],
            ["2026/09/01", "EGP/ILS", "0.0587", "0.0589", "0.0588"],
          ]))

          _(records.map { |r| [r[:base], r[:quote]] }).must_equal([["USD", "ILS"], ["GBP", "USD"], ["EGP", "ILS"]])
        end

        it "strips thousands separators and stray whitespace" do
          records = adapter.parse(workbook([
            ["2026/09/01", "USD/LBP", "89,446.50", "89,800.00", "89623.25"],
            ["2025/12/30", "XAU/USD", " 4,367.06 ", " 4,368.17 ", " 4,367.62 "],
          ]))

          _(records.map { |r| r[:rate] }).must_equal([89623.25, 4367.62])
        end

        it "skips rows with an unparseable date, pair or rate" do
          records = adapter.parse(workbook([
            ["التاريخ", "العملة", "الشراء", "البيع", "الوسطي"],
            ["2026/09/01", "USD", "1", "1", "1"],
            ["2026/09/01", "USD/ILS", "", "", ""],
            ["2026/09/01", "USD/ILS", "0", "0", "0"],
            ["2026/13/01", "USD/ILS", "3", "3", "3"],
          ]))

          _(records).must_be_empty
        end

        it "raises when the sheet has no sheetData" do
          xlsx = Zip::OutputStream.write_buffer do |zip|
            zip.put_next_entry("xl/worksheets/sheet1.xml")
            zip.write("<worksheet><dimension ref=\"A1\"/></worksheet>")
            zip.put_next_entry("xl/sharedStrings.xml")
            zip.write("<sst></sst>")
          end.string

          error = _ { adapter.parse(xlsx) }.must_raise(RuntimeError)
          _(error.message).must_include("sheetData")
        end

        it "raises when the shared strings part is missing" do
          xlsx = Zip::OutputStream.write_buffer do |zip|
            zip.put_next_entry("xl/worksheets/sheet1.xml")
            zip.write("<worksheet><sheetData/></worksheet>")
          end.string

          error = _ { adapter.parse(xlsx) }.must_raise(RuntimeError)
          _(error.message).must_include("sharedStrings")
        end

        it "raises when the workbook has no sheet" do
          empty = Zip::OutputStream.write_buffer { |zip| zip.put_next_entry("[Content_Types].xml") }.string

          error = _ { adapter.parse(empty) }.must_raise(RuntimeError)
          _(error.message).must_include("PMA")
        end
      end

      # Build a workbook shaped like the export: every cell a shared string.
      def workbook(rows)
        strings = rows.flatten.uniq
        sheet_rows = rows.map.with_index(2) do |row, r|
          cells = row.map.with_index do |value, i|
            %(<c r="#{("A".ord + i).chr}#{r}" t="s"><v>#{strings.index(value)}</v></c>)
          end
          %(<row r="#{r}">#{cells.join}</row>)
        end

        sheet_xml = <<~XML
          <?xml version="1.0" encoding="UTF-8" standalone="yes"?>
          <worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
            <sheetData>#{sheet_rows.join}</sheetData>
          </worksheet>
        XML
        strings_xml = <<~XML
          <?xml version="1.0" encoding="UTF-8" standalone="yes"?>
          <sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
            #{strings.map { |s| "<si><t>#{s}</t></si>" }.join}
          </sst>
        XML

        Zip::OutputStream.write_buffer do |zip|
          zip.put_next_entry("xl/worksheets/sheet1.xml")
          zip.write(sheet_xml)
          zip.put_next_entry("xl/sharedStrings.xml")
          zip.write(strings_xml)
        end.string
      end
    end
  end
end
