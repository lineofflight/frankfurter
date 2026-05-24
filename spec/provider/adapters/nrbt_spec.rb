# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/nrbt"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe NRBT do
      before do
        VCR.insert_cassette("nrbt", match_requests_on: [:method, :host, :path])
      end

      after { VCR.eject_cassette }

      let(:adapter) { NRBT.new }

      it "fetches rates with TOP as the base currency" do
        dataset = adapter.fetch(after: Date.new(2025, 1, 2), upto: Date.new(2025, 1, 6))

        _(dataset).wont_be_empty
        _(dataset.map { |r| r[:base] }.uniq).must_equal(["TOP"])
      end

      it "covers all twelve quote currencies in a single fetch" do
        dataset = adapter.fetch(after: Date.new(2025, 1, 2), upto: Date.new(2025, 1, 6))
        quotes = dataset.map { |r| r[:quote] }.uniq.sort

        _(quotes).must_equal(["AUD", "CAD", "CHF", "EUR", "FJD", "GBP", "JPY", "NZD", "SEK", "SGD", "USD", "WST"])
      end

      it "emits the MID column for each business day" do
        dataset = adapter.fetch(after: Date.new(2025, 1, 2), upto: Date.new(2025, 1, 2))
        usd = dataset.find { |r| r[:quote] == "USD" }

        _(usd).wont_be_nil
        # MID rate for 2025-01-02 is published as 0.4106 in column U (MID block, USD position).
        _(usd[:rate]).must_be_close_to(0.4106, 0.0001)
      end

      it "filters records by the requested date range" do
        dataset = adapter.fetch(after: Date.new(2025, 1, 2), upto: Date.new(2025, 1, 6))

        _(dataset.map { |r| r[:date] }.min).must_equal(Date.new(2025, 1, 2))
        _(dataset.map { |r| r[:date] }.max).must_equal(Date.new(2025, 1, 6))
      end

      it "skips holiday rows where rate cells hold flag text" do
        # 2025-01-01 is "Public Holiday: New Year's Day" — the rate cells contain
        # a shared-string label rather than numeric MID values, so the date is
        # absent from the output.
        dataset = adapter.fetch(after: Date.new(2024, 12, 30), upto: Date.new(2025, 1, 3))

        _(dataset.map { |r| r[:date] }.uniq).wont_include(Date.new(2025, 1, 1))
      end

      it "returns USD/TOP in a plausible range" do
        dataset = adapter.fetch(after: Date.new(2025, 1, 2), upto: Date.new(2025, 1, 2))
        usd = dataset.find { |r| r[:quote] == "USD" }

        _(usd).wont_be_nil
        # 1 TOP buys roughly 0.40-0.45 USD in 2025.
        _(usd[:rate]).must_be_close_to(0.42, 0.05)
      end

      it "reaches back to the start of the 2017 archive" do
        dataset = adapter.fetch(after: Date.new(2017, 1, 2), upto: Date.new(2017, 1, 6))

        _(dataset).wont_be_empty
        _(dataset.map { |r| r[:date] }.min).must_equal(Date.new(2017, 1, 3))
      end
    end
  end
end
