# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/mma"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe MMA do
      before do
        VCR.insert_cassette("mma", match_requests_on: [:method, :host], allow_playback_repeats: true)
      end

      after do
        VCR.eject_cassette
      end

      let(:adapter) { MMA.new }

      it "fetches rates" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 14), upto: Date.new(2026, 5, 21))

        _(dataset).wont_be_empty
        _(dataset.first[:base]).must_equal("USD")
        _(dataset.first[:quote]).must_equal("MVR")
      end

      it "filters records to the requested window" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 14), upto: Date.new(2026, 5, 21))
        dates = dataset.map { |r| r[:date] }

        _(dates.min).must_be(:>=, Date.new(2026, 5, 14))
        _(dates.max).must_be(:<=, Date.new(2026, 5, 21))
      end

      it "parses response with correct structure" do
        records = adapter.parse([
          { "Date" => "21 May 2026", "Rate" => "15.42" },
          { "Date" => "20 May 2026", "Rate" => "15.42" },
        ])

        _(records.length).must_equal(2)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("MVR")
        _(records.first[:rate]).must_equal(15.42)
        _(records.first[:date]).must_equal(Date.new(2026, 5, 21))
      end

      it "coerces numeric Rate values" do
        records = adapter.parse([
          { "Date" => "21 May 2026", "Rate" => 15.42 },
        ])

        _(records.length).must_equal(1)
        _(records.first[:rate]).must_equal(15.42)
      end

      it "skips records with missing values" do
        records = adapter.parse([
          { "Date" => "21 May 2026", "Rate" => "15.42" },
          { "Date" => "20 May 2026" },
          { "Rate" => "15.42" },
        ])

        _(records.length).must_equal(1)
      end

      it "skips zero rates" do
        records = adapter.parse([
          { "Date" => "21 May 2026", "Rate" => "0" },
          { "Date" => "20 May 2026", "Rate" => "15.42" },
        ])

        _(records.length).must_equal(1)
        _(records.first[:date]).must_equal(Date.new(2026, 5, 20))
      end

      it "deduplicates rows with the same date" do
        records = adapter.parse([
          { "Date" => "21 May 2026", "Rate" => "15.42" },
          { "Date" => "21 May 2026", "Rate" => "15.50" },
          { "Date" => "20 May 2026", "Rate" => "15.41" },
        ])

        _(records.length).must_equal(2)
        _(records.first[:date]).must_equal(Date.new(2026, 5, 21))
        _(records.first[:rate]).must_equal(15.42)
      end
    end
  end
end
