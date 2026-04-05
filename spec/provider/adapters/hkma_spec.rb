# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/hkma"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe HKMA do
      before do
        VCR.insert_cassette("hkma", match_requests_on: [:method, :host, :path])
      end

      after { VCR.eject_cassette }

      let(:adapter) { HKMA.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2026, 2, 25), upto: Date.new(2026, 2, 28))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 2, 25), upto: Date.new(2026, 2, 28))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "parses records with correct base, quote, and rate" do
        records = adapter.parse([
          {
            "end_of_day" => "2026-02-28",
            "usd" => 7.8245,
            "eur" => 9.245,
            "gbp" => 10.549,
          },
        ])

        usd = records.find { |r| r[:base] == "USD" }

        _(usd).wont_be_nil
        _(usd[:quote]).must_equal("HKD")
        _(usd[:rate]).must_equal(7.8245)
        _(usd[:date]).must_equal(Date.new(2026, 2, 28))
      end

      it "parses all currency fields" do
        record = {
          "end_of_day" => "2026-02-28",
          "usd" => 7.8245,
          "eur" => 9.245,
          "gbp" => 10.549,
          "jpy" => 0.05012,
          "cad" => 5.7355,
          "aud" => 5.5855,
          "sgd" => 6.1855,
          "twd" => 0.255,
          "chf" => 10.169,
          "cny" => 1.14015,
          "krw" => 0.005433,
          "thb" => 0.25235,
          "myr" => 2.01045,
          "php" => 0.1345,
          "inr" => 0.0855,
          "idr" => 0.0004666,
          "zar" => 0.4915,
        }

        records = adapter.parse([record])

        _(records.length).must_equal(17)
        _(records.map { |r| r[:base] }.sort).must_equal(
          ["AUD", "CAD", "CHF", "CNY", "EUR", "GBP", "IDR", "INR", "JPY", "KRW", "MYR", "PHP", "SGD", "THB", "TWD", "USD", "ZAR"],
        )
      end

      it "skips null currency values" do
        records = adapter.parse([
          {
            "end_of_day" => "1999-12-31",
            "usd" => 7.7915,
            "dem" => nil,
            "eur" => nil,
          },
        ])

        _(records.map { |r| r[:base] }).must_equal(["USD"])
      end

      it "skips records with missing end_of_day" do
        records = adapter.parse([
          { "usd" => 7.8, "eur" => 9.2 },
          { "end_of_day" => "2026-02-28", "usd" => 7.8245 },
        ])

        _(records.length).must_equal(1)
      end
    end
  end
end
