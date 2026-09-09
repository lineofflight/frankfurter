# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bcv"
require "spreadsheet"
require "stringio"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BCV do
      before do
        VCR.insert_cassette("bcv", match_requests_on: [:method, :uri])
      end

      after { VCR.eject_cassette }

      let(:adapter) { BCV.new }

      # Builds a minimal OLE2/BIFF .xls with one sheet per (value date, rows) entry, mirroring the SMC workbook layout:
      # a "Fecha Valor" banner, a two-line header whose second "Venta (ASK)" is the Bs./M.E. column, then rate rows.
      def build_xls(sheets)
        book = Spreadsheet::Workbook.new
        sheets.each do |value_date, rows|
          sheet = book.create_worksheet(name: value_date.delete("/"))
          sheet.row(0).replace([nil, "BANCO CENTRAL DE VENEZUELA"])
          sheet.row(4).replace([nil, "Fecha Operacion: 01/01/2026", nil, "Fecha Valor: #{value_date}"])
          sheet.row(7).replace([nil, nil, nil, "(a) Cotización M.E./US$", nil, "Bs./M.E."])
          sheet.row(8).replace([nil, nil, "Moneda/País", "Compra (BID)", "Venta (ASK)", "Compra (BID)", "Venta (ASK)"])
          rows.each_with_index { |values, i| sheet.row(10 + i).replace(values) }
        end
        io = StringIO.new
        book.write(io)
        io.string
      end

      it "fetches rates over a date range within one quarter" do
        dataset = adapter.fetch(after: Date.new(2026, 8, 3), upto: Date.new(2026, 8, 7))

        _(dataset).wont_be_empty
        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["VES"])
        _(dataset.map { |r| r[:date] }.min).must_be(:>=, Date.new(2026, 8, 3))
        _(dataset.map { |r| r[:date] }.max).must_be(:<=, Date.new(2026, 8, 7))
      end

      it "covers the headline currencies BCV publishes" do
        dataset = adapter.fetch(after: Date.new(2026, 8, 3), upto: Date.new(2026, 8, 7))
        bases = dataset.map { |r| r[:base] }.uniq

        ["USD", "EUR", "CNY", "TRY", "RUB"].each { |iso| _(bases).must_include(iso) }
      end

      it "returns USD/VES in a plausible range" do
        dataset = adapter.fetch(after: Date.new(2026, 8, 3), upto: Date.new(2026, 8, 7))
        usd = dataset.find { |r| r[:base] == "USD" }

        _(usd).wont_be_nil
        _(usd[:rate]).must_be(:>, 100)
        _(usd[:rate]).must_be(:<, 10_000)
      end

      it "covers each value date with a row per currency" do
        dataset = adapter.fetch(after: Date.new(2026, 8, 3), upto: Date.new(2026, 8, 7))
        by_date = dataset.group_by { |r| r[:date] }

        _(by_date.keys.size).must_equal(5)
        by_date.each_value do |records|
          bases = records.map { |r| r[:base] }

          _(bases).must_include("USD")
          _(bases.size).must_equal(bases.uniq.size)
        end
      end

      it "returns nothing before the 2021 redenomination without fetching" do
        _(adapter.fetch(after: Date.new(2020, 4, 1), upto: Date.new(2021, 9, 30))).must_be_empty
      end

      describe "#workbook_links" do
        it "keys each lettered workbook link by year and quarter, suffixed re-uploads included" do
          html = <<~HTML
            <a href="https://www.bcv.org.ve/sites/default/files/EstadisticasGeneral/2_1_2c26_smc.xls">III Trim 2026</a>
            <a href="/sites/default/files/EstadisticasGeneral/2_1_2c23_smc_60.xls">III Trim 2023</a>
            <a href="/sites/default/files/EstadisticasGeneral/2_1_2d21_smc.xls">IV Trim 2021</a>
            <a href="/sites/default/files/EstadisticasGeneral/otra_cosa.xls">Unrelated</a>
          HTML

          _(adapter.workbook_links(html)).must_equal({
            [2026, 3] => "https://www.bcv.org.ve/sites/default/files/EstadisticasGeneral/2_1_2c26_smc.xls",
            [2023, 3] => "https://www.bcv.org.ve/sites/default/files/EstadisticasGeneral/2_1_2c23_smc_60.xls",
            [2021, 4] => "https://www.bcv.org.ve/sites/default/files/EstadisticasGeneral/2_1_2d21_smc.xls",
          })
        end
      end

      describe "#parse" do
        it "emits the Bs./M.E. ask as the reference rate, foreign in base and VES in quote" do
          xls = build_xls({
            "09/09/2026" => [
              [nil, "EUR", "Zona Euro", 1.16328, 1.1633, 951.63936288, 954.02442394],
              [nil, "USD", "E.U.A.", 1.0, 1.0, 818.0515455, 820.1018],
            ],
          })

          records = adapter.parse(xls)

          _(records).must_equal([
            { date: Date.new(2026, 9, 9), base: "EUR", quote: "VES", rate: 954.02442394 },
            { date: Date.new(2026, 9, 9), base: "USD", quote: "VES", rate: 820.1018 },
          ])
        end

        it "dates each sheet by its Fecha Valor, not its name" do
          xls = build_xls({
            "07/09/2026" => [[nil, "USD", "E.U.A.", 1.0, 1.0, 812.654073, 814.6908]],
            "04/09/2026" => [[nil, "USD", "E.U.A.", 1.0, 1.0, 800.0, 802.0]],
          })

          dates = adapter.parse(xls).map { |r| r[:date] }

          _(dates).must_equal([Date.new(2026, 9, 7), Date.new(2026, 9, 4)])
        end

        it "relabels the non-ISO MXP code as MXN" do
          xls = build_xls({
            "09/09/2026" => [[nil, "MXP", "Mexico", 16.913, 16.9148, 48.36821057, 48.48943416]],
          })

          _(adapter.parse(xls).map { |r| r[:base] }).must_equal(["MXN"])
        end

        it "skips rows without a currency code or a positive rate" do
          xls = build_xls({
            "09/09/2026" => [
              [nil, "USD", "E.U.A.", 1.0, 1.0, 818.0515455, 820.1018],
              [nil, "CAD", "Canada", 1.37699, 1.37703, nil, nil],
              [nil, "JPY", "Japon", 154.168, 154.177, 0, 0],
              [nil, ""],
              [nil, "(*) Tipo de Cambio de Referencia producto de las operaciones"],
            ],
          })

          _(adapter.parse(xls).map { |r| r[:base] }).must_equal(["USD"])
        end

        it "raises when a sheet has no Fecha Valor" do
          book = Spreadsheet::Workbook.new
          sheet = book.create_worksheet(name: "09092026")
          sheet.row(8).replace([nil, nil, "Moneda/País", "Compra (BID)", "Venta (ASK)", "Compra (BID)", "Venta (ASK)"])
          sheet.row(10).replace([nil, "USD", "E.U.A.", 1.0, 1.0, 818.0515455, 820.1018])
          io = StringIO.new
          book.write(io)

          error = _ { adapter.parse(io.string) }.must_raise(RuntimeError)
          _(error.message).must_match(/Fecha Valor/)
        end

        it "raises when a sheet has no ask column" do
          book = Spreadsheet::Workbook.new
          sheet = book.create_worksheet(name: "09092026")
          sheet.row(4).replace([nil, "Fecha Operacion: 08/09/2026", nil, "Fecha Valor: 09/09/2026"])
          sheet.row(10).replace([nil, "USD", "E.U.A.", 1.0, 1.0, 818.0515455, 820.1018])
          io = StringIO.new
          book.write(io)

          error = _ { adapter.parse(io.string) }.must_raise(RuntimeError)
          _(error.message).must_match(/Venta \(ASK\)/)
        end
      end
    end
  end
end
