# frozen_string_literal: true

require_relative "../helper"
require "providers/sbi"

module Providers
  describe SBI do
    before do
      Rate.dataset.delete
      VCR.insert_cassette("sbi", match_requests_on: [:method, :host, :path, :query])
    end

    after { VCR.eject_cassette }

    let(:provider) { SBI.new }

    def count_unique_dates
      Rate.select(:date).distinct.count
    end

    it "fetches rates across multiple dates" do
      provider.fetch(since: Date.new(2026, 3, 2), upto: Date.new(2026, 3, 4)).import

      _(count_unique_dates).must_equal(3)
    end

    it "stores multiple currencies per date" do
      provider.fetch(since: Date.new(2026, 3, 2), upto: Date.new(2026, 3, 4)).import
      date = Rate.first.date

      _(Rate.where(date:).count).must_be(:>, 1)
    end

    it "parses JSON with correct base and quote" do
      json = '[{"shortName":"USD","longName":"US dollar","buyingRate":null,"askingRate":null,"midRate":137.66,"units":1}]'
      records = provider.parse(json, date: Date.new(2026, 3, 2))

      _(records.length).must_equal(1)
      _(records.first[:base]).must_equal("USD")
      _(records.first[:quote]).must_equal("ISK")
      _(records.first[:rate]).must_equal(137.66)
      _(records.first[:date]).must_equal(Date.new(2026, 3, 2))
      _(records.first[:provider]).must_equal("SBI")
    end

    it "normalizes rate by units" do
      json = '[{"shortName":"JPY","longName":"Japanese yen","buyingRate":null,"askingRate":null,"midRate":91.64,"units":100}]'
      records = provider.parse(json, date: Date.new(2026, 3, 2))

      _(records.first[:rate]).must_be_close_to(0.9164, 0.0001)
    end

    it "skips rows with nil midRate" do
      json = '[{"shortName":"USD","midRate":null,"units":1}]'
      records = provider.parse(json, date: Date.new(2026, 3, 2))

      _(records).must_be_empty
    end

    it "skips rows with invalid currency codes" do
      json = '[{"shortName":"INVALID","midRate":137.0,"units":1},{"shortName":"USD","midRate":137.0,"units":1}]'
      records = provider.parse(json, date: Date.new(2026, 3, 2))

      _(records.length).must_equal(1)
      _(records.first[:base]).must_equal("USD")
    end

    it "skips weekend dates during fetch" do
      # March 1 2026 is Sunday, only Mon–Fri should be fetched
      provider.fetch(since: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 4)).import

      dates = Rate.select_map(:date).map(&:to_s).uniq.sort

      _(dates).wont_include("2026-03-01")
    end
  end
end
