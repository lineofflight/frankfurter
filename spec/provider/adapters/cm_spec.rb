# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/cm"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe CM do
      before do
        VCR.insert_cassette("cm")
      end

      after { VCR.eject_cassette }

      let(:adapter) { CM.new }

      it "fetches daily USD reference rates for all configured crypto assets" do
        dataset = adapter.fetch(after: Date.new(2026, 10, 20), upto: Date.new(2026, 10, 22))
        dates = dataset.map { |r| r[:date] }.uniq.sort
        bases = dataset.map { |r| r[:base] }.uniq.sort

        _(dates).must_equal((Date.new(2026, 10, 20)..Date.new(2026, 10, 22)).to_a)
        _(bases).must_equal(CM::ASSETS.map(&:upcase).sort)
        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["USD"])
      end

      it "reaches Bitcoin's 2010-07-18 trading inception" do
        VCR.use_cassette("cm_coverage_start", exclusive: true) do
          dataset = adapter.fetch(after: Date.new(2010, 7, 15), upto: Date.new(2010, 7, 20))
          first = dataset.min_by { |r| r[:date] }

          _(first[:date]).must_equal(Date.new(2010, 7, 18))
          _(first[:base]).must_equal("BTC")
          _(first[:quote]).must_equal("USD")
          _(first[:rate]).must_be_close_to(0.08584, 1e-5)
        end
      end

      it "returns nothing for a window that ends before coverage" do
        _(adapter.fetch(after: Date.new(2010, 1, 1), upto: Date.new(2010, 7, 17))).must_be_empty
      end

      it "parses asset metrics and skips null or non-positive values" do
        json = <<~JSON
          {"data":[
            {"asset":"btc","time":"2010-07-18T00:00:00.000000000Z","PriceUSD":"0.08584"},
            {"asset":"btc","time":"2010-07-19T00:00:00.000000000Z","PriceUSD":null},
            {"asset":"eth","time":"2015-08-08T00:00:00.000000000Z","PriceUSD":"0"}
          ]}
        JSON

        _(adapter.parse(json)).must_equal([
          { date: Date.new(2010, 7, 18), base: "BTC", quote: "USD", rate: 0.08584 },
        ])
      end

      it "raises on a response without a data array" do
        _ { adapter.parse('{"error":{"type":"bad_parameter"}}') }.must_raise(RuntimeError)
      end
    end
  end
end
