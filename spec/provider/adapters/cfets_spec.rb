# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/cfets"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe CFETS do
      before do
        VCR.insert_cassette("cfets", match_requests_on: [:method, :uri])
      end

      after { VCR.eject_cassette }

      let(:adapter) { CFETS.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 1), upto: Date.new(2026, 9, 4))
        dates = dataset.map { |r| r[:date] }.uniq

        _(dates).must_equal([Date.new(2026, 9, 4), Date.new(2026, 9, 3), Date.new(2026, 9, 2), Date.new(2026, 9, 1)])
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 1), upto: Date.new(2026, 9, 4))
        sample = dataset.select { |r| r[:date] == Date.new(2026, 9, 4) }

        _(sample.size).must_equal(25)
      end

      it "pages through long ranges" do
        dataset = adapter.fetch(after: Date.new(2026, 6, 1), upto: Date.new(2026, 8, 31))
        dates = dataset.map { |r| r[:date] }.uniq

        _(dates.size).must_be(:>, CFETS::PAGE_SIZE)
        _(dates.min).must_equal(Date.new(2026, 6, 1))
        _(dates.max).must_equal(Date.new(2026, 8, 31))
      end

      it "parses foreign-per-CNY pairs" do
        records = adapter.parse(fixture("USD/CNY", "6.7804"))

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("CNY")
        _(records.first[:rate]).must_equal(6.7804)
      end

      it "parses CNY-per-foreign pairs" do
        records = adapter.parse(fixture("CNY/THB", "4.8863"))

        _(records.first[:base]).must_equal("CNY")
        _(records.first[:quote]).must_equal("THB")
        _(records.first[:rate]).must_equal(4.8863)
      end

      it "normalizes per-100 units" do
        records = adapter.parse(fixture("100JPY/CNY", "4.3706"))

        _(records.first[:base]).must_equal("JPY")
        _(records.first[:rate]).must_be_close_to(0.043706, 1e-9)
      end

      it "skips missing values" do
        _(adapter.parse(fixture("CNY/MOP", "---"))).must_be_empty
      end

      it "skips zero rates" do
        _(adapter.parse(fixture("USD/CNY", "0"))).must_be_empty
      end

      it "skips unrecognized labels" do
        _(adapter.parse(fixture("USD", "6.78"))).must_be_empty
      end

      it "raises when the endpoint refuses the query" do
        json = '{"data":{"head":["USD/CNY"],"flagMessage":"只提供一年历史数据查询及下载"}}'

        error = _ { adapter.parse(json) }.must_raise(RuntimeError)
        _(error.message).must_include("CFETS")
      end

      def fixture(label, value)
        <<~JSON
          {"data":{"head":["#{label}"],"total":1,"pageTotal":1},
           "records":[{"date":"2026-09-08","values":["#{value}"]}]}
        JSON
      end
    end
  end
end
