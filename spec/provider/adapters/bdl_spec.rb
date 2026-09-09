# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bdl"
require "spreadsheet"
require "stringio"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BDL do
      before do
        VCR.insert_cassette("bdl", match_requests_on: [:method, :host, :path])
      end

      after { VCR.eject_cassette }

      let(:adapter) { BDL.new }

      # Builds a minimal OLE2/BIFF .xls matching the BdL layout: one sheet per year, each with a header row and then
      # Period | Currency | Bid | Ask | Mid rows, newest first. Pass a Hash of sheet name to rows for multiple sheets.
      def build_xls(sheets)
        sheets = { "Daily Exchange Rates 2024" => sheets } if sheets.is_a?(Array)
        book = Spreadsheet::Workbook.new
        sheets.each do |name, rows|
          sheet = book.create_worksheet(name:)
          sheet.row(0).replace(["Period", "Currency", "Bid", "Ask", "Mid"])
          rows.each_with_index { |values, index| sheet.row(index + 1).replace(values) }
        end
        io = StringIO.new
        book.write(io)
        io.string
      end

      it "fetches rates with LBP as the quote currency" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 1), upto: Date.new(2026, 9, 4))

        _(dataset).wont_be_empty
        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["LBP"])
      end

      it "covers all seven base currencies in a single fetch" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 1), upto: Date.new(2026, 9, 4))

        _(dataset.map { |r| r[:base] }.uniq.sort).must_equal(["AUD", "CAD", "CHF", "EUR", "GBP", "JPY", "USD"])
      end

      it "filters records by the requested date range" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 1), upto: Date.new(2026, 9, 4))
        dates = dataset.map { |r| r[:date] }

        _(dates.min).must_equal(Date.new(2026, 9, 1))
        _(dates.max).must_equal(Date.new(2026, 9, 4))
      end

      it "emits the published mid at the 89,500 official rate" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 4), upto: Date.new(2026, 9, 4))
        usd = dataset.find { |r| r[:base] == "USD" }

        _(usd).wont_be_nil
        _(usd[:rate]).must_equal(89_500.0)
      end

      it "reads every sheet in the workbook and filters across them" do
        xls = build_xls({
          "Daily Exchange Rates  2025" => [[Date.new(2025, 1, 2), "USD", 89_500.0, 89_500.0, 89_500.0]],
          "Daily Exchange Rates 2024" => [
            [Date.new(2024, 12, 30), "USD", 89_500.0, 89_500.0, 89_500.0],
            [Date.new(2024, 1, 2), "USD", 14_935.0, 15_065.0, 15_000.0],
          ],
        })

        _(adapter.parse(xls).map { |r| [r[:date], r[:rate]] }).must_equal([
          [Date.new(2025, 1, 2), 89_500.0],
          [Date.new(2024, 12, 30), 89_500.0],
          [Date.new(2024, 1, 2), 15_000.0],
        ])
        _(adapter.parse(xls, after: Date.new(2024, 12, 1), upto: Date.new(2025, 1, 1)).map { |r| r[:date] })
          .must_equal([Date.new(2024, 12, 30)])
      end

      it "parses the published mid rather than recomputing it from bid and ask" do
        xls = build_xls([[Date.new(2024, 1, 2), "USD", 14_935.0, 15_065.0, 15_000.0]])

        _(adapter.parse(xls)).must_equal([{ date: Date.new(2024, 1, 2), base: "USD", quote: "LBP", rate: 15_000.0 }])
      end

      it "drops rows with a missing or non-positive mid" do
        xls = build_xls([
          [Date.new(2024, 1, 2), "USD", 89_500.0, 89_500.0, nil],
          [Date.new(2024, 1, 2), "EUR", 0.0, 0.0, 0.0],
          [Date.new(2024, 1, 2), "GBP", 113_000.0, 113_000.0, 113_000.0],
        ])

        _(adapter.parse(xls).map { |r| r[:base] }).must_equal(["GBP"])
      end

      it "collapses verbatim duplicate rows" do
        xls = build_xls([
          [Date.new(2025, 11, 17), "USD", 89_500.0, 89_500.0, 89_500.0],
          [Date.new(2025, 11, 17), "USD", 89_500.0, 89_500.0, 89_500.0],
        ])

        _(adapter.parse(xls).size).must_equal(1)
      end

      it "raises when the same day and currency carry different mids" do
        xls = build_xls([
          [Date.new(2025, 11, 17), "USD", 89_500.0, 89_500.0, 89_500.0],
          [Date.new(2025, 11, 17), "USD", 89_600.0, 89_600.0, 89_600.0],
        ])

        error = _ { adapter.parse(xls) }.must_raise(RuntimeError)
        _(error.message).must_include("USD 2025-11-17")
      end

      it "raises on a non-OLE2 payload" do
        _ { adapter.parse("not a workbook") }.must_raise(StandardError)
      end
    end
  end
end
