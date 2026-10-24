# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/estv"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe ESTV do
      before do
        VCR.insert_cassette("estv", match_requests_on: [:method, :uri, :body])
      end

      after { VCR.eject_cassette }

      let(:adapter) { ESTV.new }

      it "fetches crypto and currency values for each business day" do
        dataset = adapter.fetch(after: Date.new(2026, 10, 16), upto: Date.new(2026, 10, 22))
        dates = dataset.map { |r| r[:date] }.uniq.sort
        latest = dataset.select { |r| r[:date] == Date.new(2026, 10, 22) }.map { |r| r[:base] }

        _(dates).must_equal((Date.new(2026, 10, 19)..Date.new(2026, 10, 22)).to_a)
        _(latest).must_include("BTC")
        _(latest).must_include("USD")
        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["CHF"])
      end

      it "starts at the first day when a window opens before it" do
        VCR.use_cassette("estv_coverage_start", match_requests_on: [:method, :uri, :body], exclusive: true) do
          dataset = adapter.fetch(after: Date.new(2017, 12, 27), upto: Date.new(2018, 1, 3))

          _(dataset.map { |r| r[:date] }.min).must_equal(Date.new(2018, 1, 2))
          _(dataset.map { |r| r[:base] }).wont_include("BCH")
        end
      end

      it "returns nothing for a window that ends before coverage" do
        _(adapter.fetch(after: Date.new(2017, 1, 1), upto: Date.new(2018, 1, 1))).must_be_empty
      end

      it "parses token values and skips nulls" do
        json = <<~JSON
          {"status":"SUCCESS","error":null,"data":{"referenceDate":1514851200000,"tokens":[
            {"token":{"categoryShortName":"TOKEN","shortName":"BCH"},"value":null,"denomination":1},
            {"token":{"categoryShortName":"TOKEN","shortName":"BTC"},"value":14344.393055,"denomination":1}
          ]},"totalItems":null}
        JSON

        btc = { date: Date.new(2018, 1, 2), base: "BTC", quote: "CHF", rate: 14344.393055 }

        _(adapter.parse(json)).must_equal([btc])
      end

      it "normalizes currencies quoted per 100 units" do
        json = <<~JSON
          {"status":"SUCCESS","error":null,"data":{"referenceDate":1792627200000,"currencies":[
            {"currency":{"categoryShortName":"CURREN","shortName":"JPY"},"value":0.5238,"denomination":100}
          ]},"totalItems":null}
        JSON

        _(adapter.parse(json).first[:rate]).must_be_close_to(0.005238, 1e-12)
      end

      it "raises on an error response" do
        json = '{"status":"ERROR","error":"session expired","data":null}'

        _ { adapter.parse(json) }.must_raise(RuntimeError)
      end
    end
  end
end
