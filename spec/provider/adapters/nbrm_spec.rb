# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/nbrm"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe NBRM do
      before do
        VCR.insert_cassette("nbrm", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { NBRM.new }

      it "fetches rates since a date" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "excludes MKD to MKD" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1))

        _(dataset.none? { |r| r[:base] == "MKD" && r[:quote] == "MKD" }).must_equal(true)
      end

      it "parses rates from JSON" do
        json = [
          { "oznaka" => "EUR", "sreden" => "61.5", "datum" => "2026-03-01T00:00:00", "nomin" => "1" },
          { "oznaka" => "USD", "sreden" => "57.2", "datum" => "2026-03-01T00:00:00", "nomin" => "1" },
        ]

        records = adapter.parse(json)

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

        records = adapter.parse(json)

        jpy = records.find { |r| r[:base] == "JPY" }
        eur = records.find { |r| r[:base] == "EUR" }

        _(jpy[:rate]).must_be_close_to(0.388, 0.001)
        _(eur[:rate]).must_equal(61.5)
      end
    end
  end
end
