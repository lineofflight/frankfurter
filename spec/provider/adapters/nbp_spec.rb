# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/nbp"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe NBP do
      before do
        VCR.insert_cassette("nbp", match_requests_on: [:method, :uri])
      end

      after { VCR.eject_cassette }

      let(:adapter) { NBP.new }

      it "fetches rates since a date" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 5))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 5))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "includes Table B currencies" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 5))
        bases = dataset.map { |r| r[:base] }.uniq

        _(bases).must_include("ALL")
      end

      it "fetches XAU against PLN" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 5))
        gold = dataset.select { |r| r[:base] == "XAU" }

        _(gold).wont_be_empty
        gold.each { |r| _(r[:quote]).must_equal("PLN") }
      end

      it "normalizes gold rates from PLN-per-gram to PLN-per-troy-ounce" do
        json = '[{"data":"2026-04-25","cena":407.18}]'
        records = adapter.parse_gold(json)

        _(records.size).must_equal(1)
        _(records.first[:base]).must_equal("XAU")
        _(records.first[:quote]).must_equal("PLN")
        _(records.first[:date]).must_equal(Date.new(2026, 4, 25))
        _(records.first[:rate]).must_be_close_to(407.18 * Adapter::GRAMS_PER_TROY_OUNCE, 0.0001)
      end
    end
  end
end
