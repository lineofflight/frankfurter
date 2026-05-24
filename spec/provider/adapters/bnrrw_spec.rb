# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bnrrw"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BNRRW do
      before do
        VCR.insert_cassette("bnrrw", match_requests_on: [:method, :uri])
      end

      after { VCR.eject_cassette }

      let(:adapter) { BNRRW.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 19), upto: Date.new(2026, 5, 22))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 19), upto: Date.new(2026, 5, 22))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 5)
      end

      it "stores rates with foreign base and RWF quote" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 19), upto: Date.new(2026, 5, 22))

        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["RWF"])
        _(dataset.map { |r| r[:base] }.uniq).must_include("USD")
      end

      it "returns USD/RWF in a plausible range for May 2026" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 19), upto: Date.new(2026, 5, 22))
        usd = dataset.find { |r| r[:base] == "USD" && r[:date] == Date.new(2026, 5, 22) }

        _(usd).wont_be_nil
        _(usd[:rate]).must_be_close_to(1463.0, 50.0)
      end

      it "takes average_rate as the mid" do
        json = <<~JSON
          [{"currency_name":"USD","buying_rate":"1458.3525","average_rate":"1463.3525","selling_rate":"1468.3525","post_date":"22-May-26"}]
        JSON

        records = adapter.parse(json)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("RWF")
        _(records.first[:rate]).must_equal(1463.3525)
        _(records.first[:date]).must_equal(Date.new(2026, 5, 22))
      end

      it "parses values with thousands commas (older format)" do
        json = <<~JSON
          [{"currency_name":"USD","buying_rate":"1,254.96","average_rate":"1,267.50","selling_rate":"1,280.05","post_date":"04-Jan-24"}]
        JSON

        records = adapter.parse(json)

        _(records.length).must_equal(1)
        _(records.first[:rate]).must_equal(1267.50)
        _(records.first[:date]).must_equal(Date.new(2024, 1, 4))
      end

      it "skips entries with non-positive rates" do
        json = <<~JSON
          [{"currency_name":"USD","buying_rate":"0","average_rate":"0","selling_rate":"0","post_date":"22-May-26"}]
        JSON

        records = adapter.parse(json)

        _(records).must_be_empty
      end

      it "skips entries with missing average_rate" do
        json = <<~JSON
          [{"currency_name":"USD","buying_rate":"1458.0","selling_rate":"1468.0","post_date":"22-May-26"}]
        JSON

        records = adapter.parse(json)

        _(records).must_be_empty
      end

      it "returns an empty array for an empty response" do
        _(adapter.parse("[]")).must_equal([])
      end
    end
  end
end
