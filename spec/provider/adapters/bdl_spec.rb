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

      # Builds a minimal OLE2/BIFF .xls matching the BdL layout: Period | Currency | Bid | Ask | Mid, newest first.
      def build_xls(rows, name: "Daily Exchange Rates 2024")
        book = Spreadsheet::Workbook.new
        sheet = book.create_worksheet(name:)
        sheet.row(0).replace(["Period", "Currency", "Bid", "Ask", "Mid"])
        rows.each_with_index { |values, index| sheet.row(index + 1).replace(values) }
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

      it "reaches back across sheets to the start of the 2024 archive" do
        dataset = adapter.fetch(after: Date.new(2024, 1, 2), upto: Date.new(2024, 1, 3))
        usd = dataset.find { |r| r[:base] == "USD" && r[:date] == Date.new(2024, 1, 2) }

        _(usd).wont_be_nil
        # Pre-step rows carry a bid/ask spread; the published mid is 15,000.
        _(usd[:rate]).must_equal(15_000.0)
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

      it "raises on a non-OLE2 payload" do
        _ { adapter.parse("not a workbook") }.must_raise(StandardError)
      end
    end
  end
end
