# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/sbp"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe SBP do
      before do
        VCR.insert_cassette("sbp", match_requests_on: [:method, :host, :path])
      end

      after do
        VCR.eject_cassette
      end

      let(:adapter) { SBP.new }

      it "fetches rates from the archive and current files" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 1), upto: Date.new(2026, 4, 30))

        _(dataset).wont_be_empty
      end

      it "emits PKR as the quote currency" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 1), upto: Date.new(2026, 4, 30))

        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["PKR"])
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 1), upto: Date.new(2026, 4, 30))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 10)
      end

      it "returns USD/PKR in a plausible range" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 1), upto: Date.new(2026, 4, 30))
        usd = dataset.find { |r| r[:base] == "USD" && r[:date] == Date.new(2026, 4, 1) }

        _(usd).wont_be_nil
        _(usd[:rate]).must_be(:>, 100)
        _(usd[:rate]).must_be(:<, 500)
      end

      it "covers the 23 currencies listed by SBP" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 1), upto: Date.new(2026, 4, 30))
        bases = dataset.map { |r| r[:base] }.uniq.sort
        expected = [
          "AED",
          "AUD",
          "BHD",
          "CAD",
          "CHF",
          "CNY",
          "DKK",
          "EUR",
          "GBP",
          "HKD",
          "JPY",
          "KWD",
          "MYR",
          "NOK",
          "NZD",
          "OMR",
          "QAR",
          "SAR",
          "SEK",
          "SGD",
          "THB",
          "TRY",
          "USD",
        ]

        expected.each { |iso| _(bases).must_include(iso) }
      end

      it "covers the period after the mid-2026 site restructure from the archive" do
        dataset = adapter.fetch(after: Date.new(2026, 6, 1), upto: Date.new(2026, 6, 30))
        dates = dataset.map { |r| r[:date] }

        _(dates.min).must_equal(Date.new(2026, 6, 1))
        _(dates.max).must_equal(Date.new(2026, 6, 30))
      end

      it "respects the after and upto bounds" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 14), upto: Date.new(2026, 4, 17))
        dates = dataset.map { |r| r[:date] }.uniq.sort

        _(dates.min).must_be(:>=, Date.new(2026, 4, 14))
        _(dates.max).must_be(:<=, Date.new(2026, 4, 17))
      end

      describe "#parse" do
        it "maps currency-name labels to ISO codes regardless of spelling variants" do
          _(SBP::CURRENCIES["singaporian dollar"]).must_equal("SGD")
          _(SBP::CURRENCIES["singapore dollar"]).must_equal("SGD")
          _(SBP::CURRENCIES["japnese yen"]).must_equal("JPY")
          _(SBP::CURRENCIES["japanese yen"]).must_equal("JPY")
          _(SBP::CURRENCIES["uk pound sterling"]).must_equal("GBP")
          _(SBP::CURRENCIES["u.k. pound sterling"]).must_equal("GBP")
        end
      end
    end
  end
end
