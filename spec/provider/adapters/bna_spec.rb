# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bna"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BNA do
      before do
        VCR.insert_cassette("bna", match_requests_on: [:method, :uri])
      end

      after { VCR.eject_cassette }

      let(:adapter) { BNA.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 18), upto: Date.new(2026, 5, 21))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 18), upto: Date.new(2026, 5, 21))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "emits rates with AOA as quote (foreign as base)" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 18), upto: Date.new(2026, 5, 21))

        _(dataset.first[:quote]).must_equal("AOA")
        _(dataset.map { |r| r[:base] }.uniq).wont_include("AOA")
      end

      it "parses mid rates and skips non-mid rows" do
        json = <<~JSON
          {"genericResponse":[
            {"taxa":663.11983,"descricaoTipoCambio":"Taxa de Referência - MEDIO","tipoCambio":"M",
             "data":"2026-05-21","designacaoMoeda":"DOLAR CANADENSE","codigoMoeda":"CAD"},
            {"taxa":660.10,"descricaoTipoCambio":"Taxa de referencia - VENDA","tipoCambio":"B",
             "data":"2026-05-21","designacaoMoeda":"DOLAR CANADENSE","codigoMoeda":"CAD"},
            {"taxa":665.10,"descricaoTipoCambio":"Taxa de Referência - COMPRA","tipoCambio":"G",
             "data":"2026-05-21","designacaoMoeda":"DOLAR CANADENSE","codigoMoeda":"CAD"}
          ],"success":true}
        JSON

        records = adapter.parse(json)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("CAD")
        _(records.first[:quote]).must_equal("AOA")
        _(records.first[:rate]).must_be_close_to(663.11983, 0.00001)
        _(records.first[:date]).must_equal(Date.new(2026, 5, 21))
      end

      it "skips zero rates" do
        json = <<~JSON
          {"genericResponse":[
            {"taxa":0.0,"descricaoTipoCambio":"Taxa de Referência - MEDIO","tipoCambio":"M",
             "data":"2026-05-21","designacaoMoeda":"DOLAR AMERICANO","codigoMoeda":"USD"}
          ],"success":true}
        JSON

        _(adapter.parse(json)).must_be_empty
      end

      it "skips non-ISO currency codes" do
        json = <<~JSON
          {"genericResponse":[
            {"taxa":1.234,"descricaoTipoCambio":"Taxa de Referência - MEDIO","tipoCambio":"M",
             "data":"2026-05-21","designacaoMoeda":"SDR USD","codigoMoeda":"XDRUSD"}
          ],"success":true}
        JSON

        _(adapter.parse(json)).must_be_empty
      end

      it "handles empty response" do
        _(adapter.parse('{"genericResponse":[],"success":true}')).must_be_empty
      end

      it "raises on unsuccessful response" do
        error = assert_raises(RuntimeError) { adapter.parse('{"success":false,"message":"Erro"}') }

        _(error.message).must_equal("BNA: series request failed: Erro")
      end
    end
  end
end
