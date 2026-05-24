# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/nbe"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe NBE do
      before do
        VCR.insert_cassette("nbe", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { NBE.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 18), upto: Date.new(2026, 5, 20))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 18), upto: Date.new(2026, 5, 20))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "skips weekends" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 16), upto: Date.new(2026, 5, 17))

        _(dataset).must_be_empty
      end

      it "parses with foreign base and ETB quote" do
        json = <<~JSON
          {"success": true, "status": 200, "data": [
            {"buying": "159.6247", "selling": "161.2209", "date": "2026-05-21",
             "weighted_average": "159.6247",
             "currency": {"name": "US DOLLAR", "code": "USD"}}
          ]}
        JSON
        records = adapter.parse(json)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("ETB")
        _(records.first[:rate]).must_be_close_to(159.6247, 0.0001)
        _(records.first[:date]).must_equal(Date.new(2026, 5, 21))
      end

      it "uses weighted_average as the mid" do
        json = <<~JSON
          {"success": true, "status": 200, "data": [
            {"buying": "185.005", "selling": "186.8551", "date": "2026-05-21",
             "weighted_average": "185.9301",
             "currency": {"name": "EURO", "code": "EUR"}}
          ]}
        JSON
        records = adapter.parse(json)

        _(records.first[:rate]).must_be_close_to(185.9301, 0.0001)
      end

      it "passes XDR through" do
        json = <<~JSON
          {"success": true, "status": 200, "data": [
            {"buying": "218.207", "selling": "220.389", "date": "2026-05-21",
             "weighted_average": "219.298",
             "currency": {"name": "SDR", "code": "XDR"}}
          ]}
        JSON
        records = adapter.parse(json)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("XDR")
        _(records.first[:quote]).must_equal("ETB")
        _(records.first[:rate]).must_be_close_to(219.298, 0.0001)
      end

      it "skips zero rates" do
        json = <<~JSON
          {"success": true, "status": 200, "data": [
            {"buying": "0", "selling": "0", "date": "2026-05-21",
             "weighted_average": "0",
             "currency": {"name": "US DOLLAR", "code": "USD"}}
          ]}
        JSON
        records = adapter.parse(json)

        _(records).must_be_empty
      end

      it "handles empty data" do
        records = adapter.parse('{"success": true, "status": 200, "data": []}')

        _(records).must_be_empty
      end
    end
  end
end
