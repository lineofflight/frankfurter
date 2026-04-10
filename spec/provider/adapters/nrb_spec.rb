# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/nrb"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe NRB do
      before do
        VCR.insert_cassette("nrb", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { NRB.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 1), upto: Date.new(2026, 4, 5))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 1), upto: Date.new(2026, 4, 5))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_equal(22)
      end

      it "parses rates from payload" do
        payload = [
          {
            "date" => "2026-04-01",
            "rates" => [
              { "currency" => { "iso3" => "USD", "name" => "U.S. Dollar", "unit" => 1 }, "buy" => "151.44", "sell" => "152.04" },
              { "currency" => { "iso3" => "EUR", "name" => "European Euro", "unit" => 1 }, "buy" => "173.69", "sell" => "174.38" },
            ],
          },
        ]

        records = adapter.parse(payload)

        _(records.length).must_equal(2)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("NPR")
        _(records.first[:rate]).must_be_close_to(151.74, 0.01)
        _(records.first[:date]).must_equal(Date.new(2026, 4, 1))
      end

      it "normalizes rates by unit" do
        payload = [
          {
            "date" => "2026-04-01",
            "rates" => [
              { "currency" => { "iso3" => "JPY", "name" => "Japanese Yen", "unit" => 10 }, "buy" => "9.49", "sell" => "9.53" },
              { "currency" => { "iso3" => "KRW", "name" => "South Korean Won", "unit" => 100 }, "buy" => "9.92", "sell" => "9.96" },
            ],
          },
        ]

        records = adapter.parse(payload)

        jpy = records.find { |r| r[:base] == "JPY" }
        krw = records.find { |r| r[:base] == "KRW" }

        # JPY: (9.49 + 9.53) / 2.0 / 10 = 0.951
        _(jpy[:rate]).must_be_close_to(0.951, 0.001)
        # KRW: (9.92 + 9.96) / 2.0 / 100 = 0.0994
        _(krw[:rate]).must_be_close_to(0.0994, 0.0001)
      end
    end
  end
end
