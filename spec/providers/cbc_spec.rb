# frozen_string_literal: true

require_relative "../helper"
require "providers/cbc"

module Providers
  describe CBC do
    before do
      Rate.dataset.delete
      VCR.insert_cassette("cbc", match_requests_on: [:method, :host, :path])
    end

    after { VCR.eject_cassette }

    let(:provider) { CBC.new }

    def count_unique_dates
      Rate.select(:date).distinct.count
    end

    it "fetches rates with date range" do
      provider.fetch(since: Date.new(2026, 2, 1), upto: Date.new(2026, 2, 5)).import

      _(count_unique_dates).must_be(:>=, 1)
    end

    it "stores multiple currencies per date" do
      provider.fetch(since: Date.new(2026, 2, 1), upto: Date.new(2026, 2, 5)).import
      date = Rate.first.date

      _(Rate.where(date:).count).must_be(:>, 1)
    end

    it "parses rows with correct base and quote for X/USD convention" do
      rows = [
        [
          "20260201",
          "32.500",
          "150.25",
          "1.2500",
          "7.8000",
          "1200.0",
          "1.3500",
          "1.3200",
          "7.2000",
          "0.6500",
          "15800.0",
          "33.500",
          "4.4000",
          "56.000",
          "1.0500",
          "-",
          "-",
          "-",
          "25000.0",
        ],
      ]

      records = provider.parse(rows)
      twd = records.find { |r| r[:quote] == "TWD" }

      _(twd).wont_be_nil
      _(twd[:base]).must_equal("USD")
      _(twd[:rate]).must_equal(32.5)
      _(twd[:date]).must_equal(Date.new(2026, 2, 1))
    end

    it "parses rows with correct base and quote for USD/X convention" do
      rows = [
        [
          "20260201",
          "32.500",
          "150.25",
          "1.2500",
          "7.8000",
          "1200.0",
          "1.3500",
          "1.3200",
          "7.2000",
          "0.6500",
          "15800.0",
          "33.500",
          "4.4000",
          "56.000",
          "1.0500",
          "-",
          "-",
          "-",
          "25000.0",
        ],
      ]

      records = provider.parse(rows)
      gbp = records.find { |r| r[:base] == "GBP" }
      aud = records.find { |r| r[:base] == "AUD" }
      eur = records.find { |r| r[:base] == "EUR" }

      _(gbp[:quote]).must_equal("USD")
      _(gbp[:rate]).must_equal(1.25)
      _(aud[:quote]).must_equal("USD")
      _(aud[:rate]).must_equal(0.65)
      _(eur[:quote]).must_equal("USD")
      _(eur[:rate]).must_equal(1.05)
    end

    it "parses all active currency columns" do
      rows = [
        [
          "20260201",
          "32.500",
          "150.25",
          "1.2500",
          "7.8000",
          "1200.0",
          "1.3500",
          "1.3200",
          "7.2000",
          "0.6500",
          "15800.0",
          "33.500",
          "4.4000",
          "56.000",
          "1.0500",
          "-",
          "-",
          "-",
          "25000.0",
        ],
      ]

      records = provider.parse(rows)

      _(records.length).must_equal(15)
    end

    it "skips missing values marked as dash" do
      rows = [
        [
          "19930105",
          "25.405",
          "125.25",
          "1.5499",
          "7.7427",
          "788.2",
          "1.2766",
          "1.6560",
          "7.7200",
          "0.6732",
          "2048.0",
          "25.550",
          "2.5908",
          "-",
          "-",
          "1.6255",
          "5.5425",
          "1.8258",
          "-",
        ],
      ]

      records = provider.parse(rows)

      # PHP (col 13), EUR (col 14), VND (col 18) are "-", DEM/FRF/NLG excluded
      _(records.none? { |r| r[:quote] == "PHP" }).must_equal(true)
      _(records.none? { |r| r[:base] == "EUR" }).must_equal(true)
      _(records.none? { |r| r[:quote] == "VND" }).must_equal(true)
    end

    it "filters by date range" do
      rows = [
        ["20260201", "32.500", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-"],
        ["20260205", "32.600", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-"],
        ["20260210", "32.700", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-"],
      ]

      records = provider.parse(rows, Date.new(2026, 2, 3), Date.new(2026, 2, 8))

      _(records.length).must_equal(1)
      _(records.first[:date]).must_equal(Date.new(2026, 2, 5))
    end
  end
end
