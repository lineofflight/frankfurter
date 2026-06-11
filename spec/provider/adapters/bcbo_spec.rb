# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bcbo"
require "spreadsheet"
require "stringio"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BCBO do
      before do
        VCR.insert_cassette("bcbo", record: :new_episodes, match_requests_on: [:method, :uri], allow_playback_repeats: true)
      end

      after { VCR.eject_cassette }

      let(:adapter) { BCBO.new }

      # Builds a minimal OLE2/BIFF .xls matching the yearly archive layout.
      def build_xls(rows)
        book = Spreadsheet::Workbook.new
        sheet = book.create_worksheet(name: "COTIZACIONES OFICIALES 2024")
        rows.each { |index, values| sheet.row(index).replace(values) }
        io = StringIO.new
        book.write(io)
        io.string
      end

      # Builds a minimal OLE2/BIFF .xls matching the daily multi-currency layout.
      def build_daily_xls(rows)
        book = Spreadsheet::Workbook.new
        sheet = book.create_worksheet(name: "COTIZACION DE MONEDAS")
        rows.each { |index, values| sheet.row(index).replace(values) }
        io = StringIO.new
        book.write(io)
        io.string
      end

      it "fetches historical USD/BOB rates over a pre-2008 date range" do
        dataset = adapter.fetch(after: Date.new(2007, 12, 27), upto: Date.new(2007, 12, 28))

        _(dataset).wont_be_empty
        _(dataset.map { |r| r[:base] }.uniq).must_equal(["USD"])
        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["BOB"])
        _(dataset.map { |r| r[:date] }.min).must_be(:>=, Date.new(2007, 12, 27))
        _(dataset.map { |r| r[:date] }.max).must_be(:<=, Date.new(2007, 12, 28))
      end

      it "fetches daily multi-currency and metals over a post-2008 date range" do
        dataset = adapter.fetch(after: Date.new(2026, 6, 10), upto: Date.new(2026, 6, 11))

        _(dataset).wont_be_empty
        bases = dataset.map { |r| r[:base] }.uniq

        _(bases).must_include("USD")
        _(bases).must_include("EUR")
        _(bases).must_include("XAU")
        _(bases).must_include("XAG")
        _(bases).must_include("XDR")
      end

      it "averages VENTA and COMPRA to a mid with USD as base and BOB as quote in yearly files" do
        xls = build_xls({
          5 => [nil, "VENTA", "COMPRA"],
          6 => [1.0, 6.96, 6.86],
        })

        records = adapter.parse_yearly(xls, 2024)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("BOB")
        _(records.first[:rate]).must_equal(6.91)
        _(records.first[:date]).must_equal(Date.new(2024, 1, 1))
      end

      it "maps each month block to the correct date in yearly files" do
        # January in columns 1-2, March in columns 5-6.
        xls = build_xls({
          7 => [2.0, 6.96, 6.86, nil, nil, 7.00, 6.90],
        })

        records = adapter.parse_yearly(xls, 2024).sort_by { |r| r[:date] }

        _(records.map { |r| r[:date] }).must_equal([Date.new(2024, 1, 2), Date.new(2024, 3, 2)])
        _(records.map { |r| r[:rate] }).must_equal([6.91, 6.95])
      end

      it "skips the PROM average row and other non-numeric day cells in yearly files" do
        xls = build_xls({
          6 => [1.0, 6.96, 6.86],
          37 => ["PROM", "6,96", "6,86"],
        })

        records = adapter.parse_yearly(xls, 2024)

        _(records.length).must_equal(1)
        _(records.first[:date]).must_equal(Date.new(2024, 1, 1))
      end

      it "skips months with no rate for a given day in yearly files" do
        # Day 31 exists in January but not in February; the February pair is nil.
        xls = build_xls({
          36 => [31.0, 6.96, 6.86, nil, nil],
        })

        records = adapter.parse_yearly(xls, 2024)

        _(records.length).must_equal(1)
        _(records.first[:date]).must_equal(Date.new(2024, 1, 31))
      end

      it "skips invalid calendar dates in yearly files" do
        # Day 30 in February (column pair 3-4) is not a real date and is dropped.
        xls = build_xls({
          35 => [30.0, nil, nil, 6.96, 6.86],
        })

        records = adapter.parse_yearly(xls, 2024)

        _(records).must_be_empty
      end

      it "parses daily multi-currency files correctly" do
        xls = build_daily_xls({
          11 => ["ESTADOS UNIDOS", "DOLAR VENTA", "", "USD.VENTA", "6.96", ""],
          12 => ["ESTADOS UNIDOS", "DOLAR COMPRA", "", "USD.COMPRA", "6.86", ""],
          13 => ["UNION EUROPEA", "EURO", "", "EUR", "7.91", "0.86"],
          14 => ["ECUADOR", "DÓLAR", "", "USD", "6.86", "1.0"],
          15 => ["", "DERECHO ESPECIAL DE GIRO", "", "USD/D.E.G.", "", "1.36"],
          16 => ["ORO", "ONZA TROY ORO", "", "USD./O.T.F.", "4082.56"],
          17 => ["PLATA", "ONZA TROY PLATA", "", "USD./O.T.F.", "63.73"],
        })

        records = adapter.parse_daily(xls, Date.new(2026, 6, 11))

        _(records).must_include({ date: Date.new(2026, 6, 11), base: "USD", quote: "BOB", rate: 6.91 })
        _(records).must_include({ date: Date.new(2026, 6, 11), base: "EUR", quote: "BOB", rate: 7.91 })
        _(records).must_include({ date: Date.new(2026, 6, 11), base: "XDR", quote: "USD", rate: 1.36 })
        _(records).must_include({ date: Date.new(2026, 6, 11), base: "XAU", quote: "USD", rate: 4082.56 })
        _(records).must_include({ date: Date.new(2026, 6, 11), base: "XAG", quote: "USD", rate: 63.73 })

        _(records.find { |r| r[:base] == "USD" && r[:rate] == 6.86 }).must_be_nil
      end
    end
  end
end
