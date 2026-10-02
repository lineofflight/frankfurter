# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/cnb"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe CNB do
      before do
        VCR.insert_cassette("cnb", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { CNB.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 16), upto: Date.new(2026, 3, 20))

        dates = dataset.map { |r| r[:date] }.uniq

        _(dates.length).must_be(:>=, 3)
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 16), upto: Date.new(2026, 3, 20))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "returns rows when every fetched row is in range" do
        rows = [{ date: Date.new(1991, 1, 2), base: "USD", quote: "CZK", rate: 28.0 }]
        dataset = adapter.stub(:fetch_year, ->(year) { year == 1991 ? rows : [] }) do
          adapter.fetch(after: Date.new(1990, 12, 31), upto: Date.new(1991, 12, 31))
        end

        _(dataset).must_equal(rows)
      end

      it "parses JSON with correct base and quote" do
        json = {
          "rates" => [
            { "validFor" => "2026-03-17", "currencyCode" => "USD", "amount" => 1, "rate" => 22.5 },
          ],
        }

        records = adapter.parse(json)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("CZK")
        _(records.first[:rate]).must_equal(22.5)
        _(records.first[:date]).must_equal(Date.new(2026, 3, 17))
      end

      it "normalizes rates by amount" do
        json = {
          "rates" => [
            { "validFor" => "2026-03-17", "currencyCode" => "HUF", "amount" => 100, "rate" => 6.246 },
          ],
        }

        records = adapter.parse(json)

        _(records.first[:rate]).must_be_close_to(0.06246, 0.00001)
      end

      it "normalizes rates with amount 1000" do
        json = {
          "rates" => [
            { "validFor" => "2026-03-17", "currencyCode" => "IDR", "amount" => 1000, "rate" => 1.256 },
          ],
        }

        records = adapter.parse(json)

        _(records.first[:rate]).must_be_close_to(0.001256, 0.000001)
      end

      it "reads the convertible Belgian franc as BEF" do
        json = {
          "rates" => [
            { "validFor" => "1991-01-24", "currencyCode" => "BEC", "amount" => 100, "rate" => 88.99 },
            { "validFor" => "1991-01-24", "currencyCode" => "LUF", "amount" => 100, "rate" => 88.99 },
            { "validFor" => "1991-01-25", "currencyCode" => "BEF", "amount" => 100, "rate" => 89.31 },
          ],
        }

        records = adapter.parse(json)

        _(records.map { |r| r[:base] }).must_equal(["BEF", "LUF", "BEF"])
        _(records.first[:rate]).must_be_close_to(0.8899, 1e-9)
      end

      it "reads the 1991 dinar as the convertible dinar" do
        json = {
          "rates" => [
            { "validFor" => "1991-01-03", "currencyCode" => "YUD", "amount" => 1, "rate" => 2.04 },
          ],
        }

        records = adapter.parse(json)

        _(records.first[:base]).must_equal("YUN")
        _(records.first[:rate]).must_equal(2.04)
      end

      it "keeps the clearing ECU apart from the ECU" do
        json = {
          "rates" => [
            { "validFor" => "1993-03-10", "currencyCode" => "XEU", "amount" => 1, "rate" => 34.118 },
            { "validFor" => "1993-03-10", "currencyCode" => "XCU", "amount" => 1, "rate" => 33.436 },
          ],
        }

        records = adapter.parse(json)

        _(records.map { |r| [r[:base], r[:rate]] }).must_equal([["XEU", 34.118], ["XCU", 33.436]])
      end

      it "skips records with zero rate" do
        json = {
          "rates" => [
            { "validFor" => "2026-03-17", "currencyCode" => "USD", "amount" => 1, "rate" => 0 },
          ],
        }

        records = adapter.parse(json)

        _(records).must_be_empty
      end

      it "returns empty for a year before its first fixing" do
        records = adapter.parse({ "rates" => [] })

        _(records).must_be_empty
      end

      it "raises on malformed dates" do
        json = {
          "rates" => [
            { "validFor" => "not-a-date", "currencyCode" => "USD", "amount" => 1, "rate" => 22.5 },
          ],
        }

        _(-> { adapter.parse(json) }).must_raise(Date::Error)
      end
    end
  end
end
