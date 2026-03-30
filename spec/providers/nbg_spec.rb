# frozen_string_literal: true

require_relative "../helper"
require "providers/nbg"

module Providers
  describe NBG do
    before do
      Rate.dataset.delete
      VCR.insert_cassette("nbg", match_requests_on: [:method, :host])
    end

    after { VCR.eject_cassette }

    let(:provider) { NBG.new }

    def count_unique_dates
      Rate.select(:date).distinct.count
    end

    it "fetches rates with date range" do
      provider.fetch(since: Date.new(2026, 3, 2), upto: Date.new(2026, 3, 4)).import

      _(count_unique_dates).must_be(:>=, 1)
    end

    it "stores multiple currencies per date" do
      provider.fetch(since: Date.new(2026, 3, 2), upto: Date.new(2026, 3, 4)).import
      date = Rate.first.date

      _(Rate.where(date:).count).must_be(:>, 1)
    end

    it "parses currencies with correct base and quote" do
      json = <<~JSON
        [{"date": "2026-03-02T00:00:00", "currencies": [
          {"code": "USD", "quantity": 1, "rate": 2.7345, "name": "US Dollar",
           "diff": 0.001, "date": "2026-03-02T00:00:00", "validFromDate": "2026-03-03T00:00:00"}
        ]}]
      JSON
      records = provider.parse(json)

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
      records = provider.parse(json)

      _(records.first[:rate]).must_be_close_to(0.0182, 0.0001)
    end

    it "skips zero rates" do
      json = <<~JSON
        [{"date": "2026-03-02T00:00:00", "currencies": [
          {"code": "USD", "quantity": 1, "rate": 0.0, "name": "US Dollar",
           "diff": 0.0, "date": "2026-03-02T00:00:00", "validFromDate": "2026-03-03T00:00:00"}
        ]}]
      JSON
      records = provider.parse(json)

      _(records).must_be_empty
    end

    it "skips invalid currency codes" do
      json = <<~JSON
        [{"date": "2026-03-02T00:00:00", "currencies": [
          {"code": "XX", "quantity": 1, "rate": 1.5, "name": "Invalid",
           "diff": 0.0, "date": "2026-03-02T00:00:00", "validFromDate": "2026-03-03T00:00:00"}
        ]}]
      JSON
      records = provider.parse(json)

      _(records).must_be_empty
    end

    it "handles empty response" do
      records = provider.parse("[]")

      _(records).must_be_empty
    end
  end
end
