# frozen_string_literal: true

require_relative "../helper"
require "providers/bnm"

module Providers
  describe BNM do
    before do
      Rate.dataset.delete
      VCR.insert_cassette("bnm")
    end

    after do
      VCR.eject_cassette
    end

    let(:provider) { BNM.new }

    def count_unique_dates
      Rate.select(:date).distinct.count
    end

    it "fetches rates" do
      provider.fetch(since: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 31)).import

      _(count_unique_dates).must_be(:>, 1)
    end

    it "stores multiple currencies per date" do
      provider.fetch(since: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 31)).import
      date = Rate.first.date

      _(Rate.where(date:).count).must_be(:>, 1)
    end

    it "normalizes rates by unit" do
      provider.fetch(since: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 31)).import
      jpy_rate = Rate.where(base: "JPY").first

      _(jpy_rate).wont_be_nil
      _(jpy_rate.rate).must_be(:<, 1)
    end

    it "imports net-new currencies" do
      provider.fetch(since: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 31)).import

      ["BND", "EGP", "KHR", "MMK", "NPR"].each do |code|
        _(Rate.where(base: code).count).must_be(:>, 0, "expected #{code} rates")
      end
    end

    it "respects upto date" do
      provider.fetch(since: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 10)).import

      _(Rate.where(Sequel[:date] > Date.new(2026, 3, 10)).count).must_equal(0)
      _(Rate.where(Sequel[:date] <= Date.new(2026, 3, 10)).count).must_be(:>, 0)
    end

    it "excludes SDR" do
      provider.fetch(since: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 31)).import

      _(Rate.where(base: "SDR").count).must_equal(0)
      _(Rate.where(quote: "SDR").count).must_equal(0)
    end
  end
end
