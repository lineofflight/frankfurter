# frozen_string_literal: true

require_relative "../helper"
require "providers/nbrm"

module Providers
  describe NBRM do
    before do
      Rate.dataset.delete
      VCR.insert_cassette("nbrm", match_requests_on: [:method, :host])
    end

    after { VCR.eject_cassette }

    let(:provider) { NBRM.new }

    def count_unique_dates
      Rate.select(:date).distinct.count
    end

    it "fetches rates since a date" do
      provider.fetch(since: Date.new(2026, 3, 1)).import

      _(count_unique_dates).must_be(:>, 1)
    end

    it "stores multiple currencies per date" do
      provider.fetch(since: Date.new(2026, 3, 1)).import
      date = Rate.first.date

      _(Rate.where(date:).count).must_be(:>, 1)
    end

    it "excludes MKD to MKD" do
      provider.fetch(since: Date.new(2026, 3, 1)).import

      _(Rate.where(base: "MKD", quote: "MKD").count).must_equal(0)
    end

    it "parses rates from JSON" do
      json = [
        { "oznaka" => "EUR", "sreden" => "61.5", "datum" => "2026-03-01T00:00:00", "nomin" => "1" },
        { "oznaka" => "USD", "sreden" => "57.2", "datum" => "2026-03-01T00:00:00", "nomin" => "1" },
      ]

      records = provider.parse(json)

      _(records.length).must_equal(2)
      _(records.first[:base]).must_equal("EUR")
      _(records.first[:quote]).must_equal("MKD")
      _(records.first[:rate]).must_equal(61.5)
    end

    it "normalizes rates when nomin is greater than 1" do
      json = [
        { "oznaka" => "JPY", "sreden" => "38.8", "datum" => "2010-01-04T00:00:00", "nomin" => "100" },
        { "oznaka" => "EUR", "sreden" => "61.5", "datum" => "2010-01-04T00:00:00", "nomin" => "1" },
      ]

      records = provider.parse(json)

      jpy = records.find { |r| r[:base] == "JPY" }
      eur = records.find { |r| r[:base] == "EUR" }

      _(jpy[:rate]).must_be_close_to(0.388, 0.001)
      _(eur[:rate]).must_equal(61.5)
    end
  end
end
