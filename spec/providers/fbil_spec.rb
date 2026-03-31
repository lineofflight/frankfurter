# frozen_string_literal: true

require_relative "../helper"
require "providers/fbil"

module Providers
  describe FBIL do
    before do
      Rate.dataset.delete
      VCR.insert_cassette("fbil", match_requests_on: [:method, :host])
    end

    after { VCR.eject_cassette }

    let(:provider) { FBIL.new }

    def count_unique_dates
      Rate.select(:date).distinct.count
    end

    it "fetches rates with date range" do
      provider.fetch(since: Date.new(2026, 3, 17), upto: Date.new(2026, 3, 21)).import

      _(count_unique_dates).must_be(:>=, 1)
    end

    it "stores multiple currencies per date" do
      provider.fetch(since: Date.new(2026, 3, 17), upto: Date.new(2026, 3, 21)).import
      date = Rate.first.date

      _(Rate.where(date:).count).must_be(:>, 1)
    end

    it "parses JSON with correct base and quote" do
      json = [
        {
          "processRunDate" => "2026-03-17 00:00:00",
          "subProdName" => "INR / 1 USD",
          "displayTime" => "2026-03-17 13:00:00",
          "rate" => 92.457,
          "comments" => "",
        },
      ]

      records = provider.parse(json)

      _(records.length).must_equal(1)
      _(records.first[:base]).must_equal("USD")
      _(records.first[:quote]).must_equal("INR")
      _(records.first[:rate]).must_equal(92.457)
      _(records.first[:date]).must_equal(Date.new(2026, 3, 17))
    end

    it "adjusts rate by unit multiplier for JPY" do
      json = [
        {
          "processRunDate" => "2026-03-17 00:00:00",
          "subProdName" => "INR / 100 JPY",
          "displayTime" => "2026-03-17 13:00:00",
          "rate" => 57.99,
          "comments" => "",
        },
      ]

      records = provider.parse(json)

      _(records.first[:base]).must_equal("JPY")
      _(records.first[:rate]).must_be_close_to(0.5799, 0.0001)
    end

    it "adjusts rate by unit multiplier for IDR" do
      json = [
        {
          "processRunDate" => "2026-03-17 00:00:00",
          "subProdName" => "INR / 10000 IDR",
          "displayTime" => "2026-03-17 13:00:00",
          "rate" => 54.4093,
          "comments" => "",
        },
      ]

      records = provider.parse(json)

      _(records.first[:base]).must_equal("IDR")
      _(records.first[:rate]).must_be_close_to(0.00544093, 0.00000001)
    end

    it "skips records with zero rate" do
      json = [
        {
          "processRunDate" => "2026-03-17 00:00:00",
          "subProdName" => "INR / 1 USD",
          "displayTime" => "2026-03-17 13:00:00",
          "rate" => 0.0,
          "comments" => "",
        },
      ]

      records = provider.parse(json)

      _(records).must_be_empty
    end

    it "skips records with missing subProdName" do
      json = [
        {
          "processRunDate" => "2026-03-17 00:00:00",
          "subProdName" => nil,
          "displayTime" => "2026-03-17 13:00:00",
          "rate" => 92.457,
          "comments" => "",
        },
      ]

      records = provider.parse(json)

      _(records).must_be_empty
    end

    it "skips records with non-numeric rate" do
      json = [
        {
          "processRunDate" => "2026-03-17 00:00:00",
          "subProdName" => "INR / 1 USD",
          "displayTime" => "2026-03-17 13:00:00",
          "rate" => "N/A",
          "comments" => "",
        },
      ]

      records = provider.parse(json)

      _(records).must_be_empty
    end
  end
end
