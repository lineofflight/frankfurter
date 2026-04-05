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

      it "skips records with zero rate" do
        json = {
          "rates" => [
            { "validFor" => "2026-03-17", "currencyCode" => "USD", "amount" => 1, "rate" => 0 },
          ],
        }

        records = adapter.parse(json)

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
