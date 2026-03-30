# frozen_string_literal: true

require_relative "../helper"
require "providers/rb"

module Providers
  describe RB do
    before do
      Rate.dataset.delete
      VCR.insert_cassette("rb", match_requests_on: [:method, :host])
    end

    after { VCR.eject_cassette }

    let(:provider) { RB.new }

    def count_unique_dates
      Rate.select(:date).distinct.count
    end

    it "fetches rates with date range" do
      provider.fetch(since: Date.new(2026, 3, 24), upto: Date.new(2026, 3, 28)).import

      _(count_unique_dates).must_be(:>=, 1)
    end

    it "stores multiple currencies per date" do
      provider.fetch(since: Date.new(2026, 3, 24), upto: Date.new(2026, 3, 28)).import
      date = Rate.first.date

      _(Rate.where(date:).count).must_be(:>, 1)
    end

    it "parses JSON with correct base and quote" do
      json = '[{"date":"2026-03-24","value":10.8238}]'

      records = provider.parse(json, currency: "EUR")

      _(records.length).must_equal(1)
      _(records.first[:base]).must_equal("EUR")
      _(records.first[:quote]).must_equal("SEK")
      _(records.first[:rate]).must_equal(10.8238)
      _(records.first[:date]).must_equal(Date.new(2026, 3, 24))
    end

    it "skips records with zero rate" do
      json = '[{"date":"2026-03-24","value":0}]'

      records = provider.parse(json, currency: "EUR")

      _(records).must_be_empty
    end

    it "skips records with missing values" do
      json = '[{"date":"2026-03-24","value":null}]'

      records = provider.parse(json, currency: "EUR")

      _(records).must_be_empty
    end
  end
end
