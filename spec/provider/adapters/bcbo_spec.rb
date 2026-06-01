# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bcbo"
require "spreadsheet"
require "stringio"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BCBO do
      before do
        VCR.insert_cassette("bcbo", match_requests_on: [:method, :uri], allow_playback_repeats: true)
      end

      after { VCR.eject_cassette }

      let(:adapter) { BCBO.new }

      # Builds a minimal OLE2/BIFF .xls matching the yearly archive layout:
      # column A holds the day of month, then twelve month blocks of two columns
      # (VENTA, COMPRA). Returns the raw bytes so parse exercises a real read.
      def build_xls(rows)
        book = Spreadsheet::Workbook.new
        sheet = book.create_worksheet(name: "COTIZACIONES OFICIALES 2024")
        rows.each { |index, values| sheet.row(index).replace(values) }
        io = StringIO.new
        book.write(io)
        io.string
      end

      it "fetches rates over a date range" do
        dataset = adapter.fetch(after: Date.new(2024, 1, 2), upto: Date.new(2024, 1, 10))

        _(dataset).wont_be_empty
        _(dataset.map { |r| r[:base] }.uniq).must_equal(["USD"])
        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["BOB"])
        _(dataset.map { |r| r[:date] }.min).must_be(:>=, Date.new(2024, 1, 2))
        _(dataset.map { |r| r[:date] }.max).must_be(:<=, Date.new(2024, 1, 10))
      end

      it "averages VENTA and COMPRA to a mid with USD as base and BOB as quote" do
        xls = build_xls({
          5 => [nil, "VENTA", "COMPRA"],
          6 => [1.0, 6.96, 6.86],
        })

        records = adapter.parse(xls, 2024)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("BOB")
        _(records.first[:rate]).must_equal(6.91)
        _(records.first[:date]).must_equal(Date.new(2024, 1, 1))
      end

      it "maps each month block to the correct date" do
        # January in columns 1-2, March in columns 5-6.
        xls = build_xls({
          7 => [2.0, 6.96, 6.86, nil, nil, 7.00, 6.90],
        })

        records = adapter.parse(xls, 2024).sort_by { |r| r[:date] }

        _(records.map { |r| r[:date] }).must_equal([Date.new(2024, 1, 2), Date.new(2024, 3, 2)])
        _(records.map { |r| r[:rate] }).must_equal([6.91, 6.95])
      end

      it "skips the PROM average row and other non-numeric day cells" do
        xls = build_xls({
          6 => [1.0, 6.96, 6.86],
          37 => ["PROM", "6,96", "6,86"],
        })

        records = adapter.parse(xls, 2024)

        _(records.length).must_equal(1)
        _(records.first[:date]).must_equal(Date.new(2024, 1, 1))
      end

      it "skips months with no rate for a given day" do
        # Day 31 exists in January but not in February; the February pair is nil.
        xls = build_xls({
          36 => [31.0, 6.96, 6.86, nil, nil],
        })

        records = adapter.parse(xls, 2024)

        _(records.length).must_equal(1)
        _(records.first[:date]).must_equal(Date.new(2024, 1, 31))
      end

      it "skips invalid calendar dates" do
        # Day 30 in February (column pair 3-4) is not a real date and is dropped.
        xls = build_xls({
          35 => [30.0, nil, nil, 6.96, 6.86],
        })

        records = adapter.parse(xls, 2024)

        _(records).must_be_empty
      end
    end
  end
end
