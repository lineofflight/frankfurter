# frozen_string_literal: true

require_relative "../helper"
require "providers/rb"

module Providers
  describe RB do
    before do
      Rate.dataset.delete
      VCR.insert_cassette("rb")
    end

    after { VCR.eject_cassette }

    let(:provider) { RB.new }

    it "fetches and imports rates" do
      provider.fetch(since: Date.new(2026, 3, 24), upto: Date.new(2026, 3, 28)).import

      dates = Rate.select(:date).distinct.map(:date)

      _(dates.length).must_be(:>=, 3)

      currencies = Rate.select(:base).distinct.map(:base)

      _(currencies).must_include("EUR")
      _(currencies).must_include("USD")
      _(currencies.length).must_be(:>=, 20)
    end

    it "parses observations from ByGroup response" do
      json = [
        { "seriesId" => "SEKEURPMI", "date" => "2026-03-24", "value" => 10.8238 },
        { "seriesId" => "SEKUSDPMI", "date" => "2026-03-24", "value" => 9.9421 },
      ]

      records = provider.parse(json)

      _(records.length).must_equal(2)
      _(records.first[:base]).must_equal("EUR")
      _(records.first[:quote]).must_equal("SEK")
      _(records.first[:rate]).must_equal(10.8238)
      _(records.last[:base]).must_equal("USD")
    end

    it "filters out SEKETT identity rate" do
      json = [
        { "seriesId" => "SEKETT", "date" => "2026-03-24", "value" => 1.0 },
        { "seriesId" => "SEKEURPMI", "date" => "2026-03-24", "value" => 10.8238 },
      ]

      records = provider.parse(json)

      _(records.length).must_equal(1)
      _(records.first[:base]).must_equal("EUR")
    end

    it "skips records with zero rate" do
      json = [{ "seriesId" => "SEKEURPMI", "date" => "2026-03-24", "value" => 0 }]

      _(provider.parse(json)).must_be_empty
    end

    it "skips records with missing values" do
      json = [{ "seriesId" => "SEKEURPMI", "date" => "2026-03-24", "value" => nil }]

      _(provider.parse(json)).must_be_empty
    end

    it "returns empty array for non-array response" do
      _(provider.parse("Not found")).must_be_empty
    end
  end
end
