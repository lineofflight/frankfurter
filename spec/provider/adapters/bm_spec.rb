# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bm"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BM do
      before do
        VCR.insert_cassette("bm", match_requests_on: [:method, :uri])
      end

      after { VCR.eject_cassette }

      let(:adapter) { BM.new }

      it "fetches rates with MZN as the quote currency" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 2), upto: Date.new(2026, 9, 4))

        _(dataset).wont_be_empty
        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["MZN"])
      end

      it "covers all 19 currencies in the bulletin" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 4), upto: Date.new(2026, 9, 4))
        bases = dataset.map { |r| r[:base] }.sort

        _(bases).must_equal([
          "BRL", "BWP", "CAD", "CHF", "CNH", "CNY", "DKK", "EUR", "GBP", "JPY",
          "MUR", "MWK", "NOK", "SEK", "SZL", "TZS", "USD", "ZAR", "ZMW",
        ])
      end

      it "emits the published mid, not buy or sell" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 4), upto: Date.new(2026, 9, 4))
        usd = dataset.find { |r| r[:base] == "USD" }

        _(usd[:rate]).must_equal(63.91)
      end

      it "rescales the per-1000 block to per-unit" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 4), upto: Date.new(2026, 9, 4))
        jpy = dataset.find { |r| r[:base] == "JPY" }

        _(jpy[:rate]).must_be_close_to(0.41013, 1e-9)
      end

      it "keeps onshore and offshore renminbi apart" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 4), upto: Date.new(2026, 9, 4))
        cny = dataset.find { |r| r[:base] == "CNY" }
        cnh = dataset.find { |r| r[:base] == "CNH" }

        _(cny[:rate]).must_equal(9.52)
        _(cnh[:rate]).must_equal(9.53)
      end

      it "filters records by the requested date range" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 2), upto: Date.new(2026, 9, 4))
        dates = dataset.map { |r| r[:date] }.uniq.sort

        _(dates).must_equal([Date.new(2026, 9, 2), Date.new(2026, 9, 3), Date.new(2026, 9, 4)])
      end

      describe "#parse" do
        let(:date) { Date.new(2018, 1, 2) }

        # Bulletins before November 2020 carry buy and sell only.
        let(:text) do
          <<~TEXT
                                                    MERCADO CAMBIAL
                                                  BOLETIM Nº  002/18
            1. TAXAS DE CÂMBIO MÉDIAS DE REFERÊNCIA EM METICAIS DO DIA 02 Janeiro
               de 2018

                                                           CÂMBIOS(MT)
            PAÍSES                  MOEDAS           COMPRA          VENDA

            Estados Unidos(a)       Dolar               58,40          59,56

            2.   OUTRAS TAXAS MÉDIAS (b)
            2.1. PAÍSES VIZINHOS
            2.1.1 Meticais por Unidade de Moeda
                   PAÍSES           MOEDAS
              Àfrica do Sul         Rand                 4,74            4,83
              Swazilândia           Lilangueni           4,74            4,83

            2.1.2 Meticais por 1000 Unidades de Moeda

                   PAÍSES           MOEDAS
              Japão                 Iene               519,79         530,09
              Zimbabwe              Dólar              154,50         157,56

            2.2. OUTROS PAÍSES
            2.2.1 Meticais por Unidade de Moeda
                    PAÍSES          MOEDAS
              China/Offshore        Rememb               9,00            9,18
              China                 Rememb               9,00            9,17

            3. OUTRAS INFORMAÇÕES

            1. PRIME RATE - Nova Iorque.......................     4,5000000   %
            3. OURO/-USD/Onça:
            Compra.............   1.311,91000
            Venda..............   1.312,68000
          TEXT
        end

        it "synthesises the mid from buy and sell when no mid column is published" do
          usd = adapter.parse(text, date).find { |r| r[:base] == "USD" }

          _(usd[:rate]).must_equal(58.98)
        end

        it "rescales per-1000 rows and resets to per-unit at the next heading" do
          records = adapter.parse(text, date)
          jpy = records.find { |r| r[:base] == "JPY" }
          cny = records.find { |r| r[:base] == "CNY" }

          _(jpy[:rate]).must_be_close_to(0.52494, 1e-9)
          _(cny[:rate]).must_equal(9.085)
        end

        it "maps the former name of eSwatini" do
          szl = adapter.parse(text, date).find { |r| r[:base] == "SZL" }

          _(szl[:rate]).must_equal(4.785)
        end

        it "drops the Zimbabwe row and everything after the rates table" do
          records = adapter.parse(text, date)

          _(records.map { |r| r[:base] }).must_equal(["USD", "ZAR", "SZL", "JPY", "CNH", "CNY"])
        end

        it "stamps every record with the given date" do
          _(adapter.parse(text, date).map { |r| r[:date] }.uniq).must_equal([date])
        end
      end
    end
  end
end
