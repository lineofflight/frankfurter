# frozen_string_literal: true

require_relative "../helper"
require "providers/cnb"

module Providers
  describe CNB do
    before do
      Rate.dataset.delete
      VCR.insert_cassette("cnb", match_requests_on: [:method, :host])
    end

    after { VCR.eject_cassette }

    let(:provider) { CNB.new }

    def count_unique_dates
      Rate.select(:date).distinct.count
    end

    it "fetches rates with date range" do
      provider.fetch(since: Date.new(2026, 3, 16), upto: Date.new(2026, 3, 20)).import

      _(count_unique_dates).must_be(:>=, 3)
    end

    it "stores multiple currencies per date" do
      provider.fetch(since: Date.new(2026, 3, 16), upto: Date.new(2026, 3, 20)).import
      date = Rate.first.date

      _(Rate.where(date:).count).must_be(:>, 1)
    end

    it "parses JSON with correct base and quote" do
      json = {
        "rates" => [
          { "validFor" => "2026-03-17", "currencyCode" => "USD", "amount" => 1, "rate" => 22.5 },
        ],
      }

      records = provider.parse(json)

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

      records = provider.parse(json)

      _(records.first[:rate]).must_be_close_to(0.06246, 0.00001)
    end

    it "normalizes rates with amount 1000" do
      json = {
        "rates" => [
          { "validFor" => "2026-03-17", "currencyCode" => "IDR", "amount" => 1000, "rate" => 1.256 },
        ],
      }

      records = provider.parse(json)

      _(records.first[:rate]).must_be_close_to(0.001256, 0.000001)
    end

    it "skips records with zero rate" do
      json = {
        "rates" => [
          { "validFor" => "2026-03-17", "currencyCode" => "USD", "amount" => 1, "rate" => 0 },
        ],
      }

      records = provider.parse(json)

      _(records).must_be_empty
    end

    it "skips records with malformed dates" do
      json = {
        "rates" => [
          { "validFor" => "not-a-date", "currencyCode" => "USD", "amount" => 1, "rate" => 22.5 },
          { "validFor" => "2026-03-17", "currencyCode" => "EUR", "amount" => 1, "rate" => 24.4 },
        ],
      }

      records = provider.parse(json)

      _(records.length).must_equal(1)
      _(records.first[:base]).must_equal("EUR")
    end
  end
end
