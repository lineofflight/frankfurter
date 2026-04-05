# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/nbg"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe NBG do
      before do
        VCR.insert_cassette("nbg", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { NBG.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 2), upto: Date.new(2026, 3, 4))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 2), upto: Date.new(2026, 3, 4))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "parses currencies with correct base and quote" do
        json = <<~JSON
          [{"date": "2026-03-02T00:00:00", "currencies": [
            {"code": "USD", "quantity": 1, "rate": 2.7345, "name": "US Dollar",
             "diff": 0.001, "date": "2026-03-02T00:00:00", "validFromDate": "2026-03-03T00:00:00"}
          ]}]
        JSON
        records = adapter.parse(json)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("GEL")
        _(records.first[:rate]).must_be_close_to(2.7345, 0.0001)
      end

      it "normalizes rate by quantity" do
        json = <<~JSON
          [{"date": "2026-03-02T00:00:00", "currencies": [
            {"code": "JPY", "quantity": 100, "rate": 1.8200, "name": "Japanese Yen",
             "diff": 0.001, "date": "2026-03-02T00:00:00", "validFromDate": "2026-03-03T00:00:00"}
          ]}]
        JSON
        records = adapter.parse(json)

        _(records.first[:rate]).must_be_close_to(0.0182, 0.0001)
      end

      it "skips zero rates" do
        json = <<~JSON
          [{"date": "2026-03-02T00:00:00", "currencies": [
            {"code": "USD", "quantity": 1, "rate": 0.0, "name": "US Dollar",
             "diff": 0.0, "date": "2026-03-02T00:00:00", "validFromDate": "2026-03-03T00:00:00"}
          ]}]
        JSON
        records = adapter.parse(json)

        _(records).must_be_empty
      end

      it "skips invalid currency codes" do
        json = <<~JSON
          [{"date": "2026-03-02T00:00:00", "currencies": [
            {"code": "XX", "quantity": 1, "rate": 1.5, "name": "Invalid",
             "diff": 0.0, "date": "2026-03-02T00:00:00", "validFromDate": "2026-03-03T00:00:00"}
          ]}]
        JSON
        records = adapter.parse(json)

        _(records).must_be_empty
      end

      it "handles empty response" do
        records = adapter.parse("[]")

        _(records).must_be_empty
      end
    end
  end
end
