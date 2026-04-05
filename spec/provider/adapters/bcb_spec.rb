# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bcb"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BCB do
      let(:adapter) { BCB.new }
      before { VCR.insert_cassette("bcb") }
      after { VCR.eject_cassette }

      it "fetches rates since a date" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 7))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 7))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      describe "parse" do
        it "parses a normal response" do
          json = {
            value: [
              {
                cotacaoCompra: 5.1995,
                cotacaoVenda: 5.2001,
                dataHoraCotacao: "2026-03-02 13:09:26.433",
                tipoBoletim: "Fechamento",
              },
            ],
          }.to_json

          rates = adapter.parse(json, "USD")

          _(rates.length).must_equal(1)
          _(rates[0][:base]).must_equal("USD")
          _(rates[0][:quote]).must_equal("BRL")
          _(rates[0][:rate]).must_be_close_to(5.2001)
        end

        it "extracts the date from dataHoraCotacao" do
          json = {
            value: [
              {
                cotacaoVenda: 5.2001,
                dataHoraCotacao: "2026-03-04 13:09:26.433",
                tipoBoletim: "Fechamento",
              },
            ],
          }.to_json

          rates = adapter.parse(json, "USD")

          _(rates[0][:date]).must_equal(Date.new(2026, 3, 4))
        end

        it "returns empty array for empty response" do
          json = { value: [] }.to_json

          rates = adapter.parse(json, "USD")

          _(rates).must_be_empty
        end

        it "skips records with missing cotacaoVenda" do
          json = {
            value: [
              {
                cotacaoCompra: 5.1995,
                cotacaoVenda: nil,
                dataHoraCotacao: "2026-03-02 13:09:26.433",
                tipoBoletim: "Fechamento",
              },
            ],
          }.to_json

          rates = adapter.parse(json, "USD")

          _(rates).must_be_empty
        end
      end
    end
  end
end
