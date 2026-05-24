# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/cbg"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe CBG do
      before do
        VCR.insert_cassette("cbg", match_requests_on: [:method, :host, :path])
      end

      after { VCR.eject_cassette }

      let(:adapter) { CBG.new }

      it "fetches rates within a narrow date window" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 19), upto: Date.new(2026, 5, 22))

        _(dataset).wont_be_empty
      end

      it "respects after and upto filters" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 19), upto: Date.new(2026, 5, 22))
        dates = dataset.map { |r| r[:date] }.uniq

        _(dates.min).must_be(:>=, Date.new(2026, 5, 20))
        _(dates.max).must_be(:<=, Date.new(2026, 5, 22))
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 19), upto: Date.new(2026, 5, 22))
        first_date = dataset.map { |r| r[:date] }.min
        sample = dataset.select { |r| r[:date] == first_date }

        _(sample.size).must_be(:>, 5)
      end

      it "stores foreign currency as base and GMD as quote" do
        records = adapter.parse([[1779408000000, 72.39]], "USD")
        usd = records.first

        _(usd[:base]).must_equal("USD")
        _(usd[:quote]).must_equal("GMD")
        _(usd[:rate]).must_equal(72.39)
        _(usd[:date]).must_equal(Date.new(2026, 5, 22))
      end

      it "parses millisecond epoch timestamps as UTC dates" do
        records = adapter.parse([[1779408000000, 86.28]], "EUR")

        _(records.first[:date]).must_equal(Date.new(2026, 5, 22))
      end

      it "skips zero rates" do
        records = adapter.parse([[1779408000000, 0.0]], "USD")

        _(records).must_be_empty
      end

      it "handles an empty response" do
        records = adapter.parse([], "USD")

        _(records).must_be_empty
      end

      it "skips malformed entries" do
        records = adapter.parse(
          [
            [1779408000000, 72.39],
            [nil, 1.0],
            [1779408000000, nil],
            "not an entry",
          ],
          "USD",
        )

        _(records.size).must_equal(1)
        _(records.first[:rate]).must_equal(72.39)
      end

      it "returns USD/GMD in a plausible range for May 2026" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 21), upto: Date.new(2026, 5, 22))
        usd = dataset.find { |r| r[:base] == "USD" && r[:date] == Date.new(2026, 5, 22) }

        _(usd).wont_be_nil
        _(usd[:rate]).must_be_close_to(72.39, 5.0)
      end

      it "excludes the non-ISO WAUA series" do
        _(CBG::CURRENCIES).wont_include("WAUA")
      end

      it "does not request the GMD pivot itself" do
        _(CBG::CURRENCIES).wont_include("GMD")
      end
    end
  end
end
