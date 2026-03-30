# frozen_string_literal: true

require_relative "../helper"
require "providers/hkma"

module Providers
  describe HKMA do
    before do
      Rate.dataset.delete
      VCR.insert_cassette("hkma", match_requests_on: [:method, :host, :path], allow_playback_repeats: true)
    end

    after { VCR.eject_cassette }

    let(:provider) { HKMA.new }

    def count_unique_dates
      Rate.select(:date).distinct.count
    end

    it "fetches rates with date range" do
      provider.fetch(since: Date.new(2026, 2, 1), upto: Date.new(2026, 2, 28)).import

      _(count_unique_dates).must_be(:>=, 1)
    end

    it "stores multiple currencies per date" do
      provider.fetch(since: Date.new(2026, 2, 1), upto: Date.new(2026, 2, 28)).import
      date = Rate.first.date

      _(Rate.where(date:).count).must_be(:>, 1)
    end

    it "parses records with correct base, quote, and rate" do
      records = provider.parse([
        {
          "end_of_day" => "2026-02-28",
          "usd" => 7.8245,
          "eur" => 9.245,
          "gbp" => 10.549,
        },
      ])

      usd = records.find { |r| r[:base] == "USD" }

      _(usd).wont_be_nil
      _(usd[:quote]).must_equal("HKD")
      _(usd[:rate]).must_equal(7.8245)
      _(usd[:date]).must_equal(Date.new(2026, 2, 28))
    end

    it "parses all currency fields" do
      record = {
        "end_of_day" => "2026-02-28",
        "usd" => 7.8245,
        "eur" => 9.245,
        "gbp" => 10.549,
        "jpy" => 0.05012,
        "cad" => 5.7355,
        "aud" => 5.5855,
        "sgd" => 6.1855,
        "twd" => 0.255,
        "chf" => 10.169,
        "cny" => 1.14015,
        "krw" => 0.005433,
        "thb" => 0.25235,
        "myr" => 2.01045,
        "php" => 0.1345,
        "inr" => 0.0855,
        "idr" => 0.0004666,
        "zar" => 0.4915,
      }

      records = provider.parse([record])

      _(records.length).must_equal(17)
      _(records.map { |r| r[:base] }.sort).must_equal(
        ["AUD", "CAD", "CHF", "CNY", "EUR", "GBP", "IDR", "INR", "JPY", "KRW", "MYR", "PHP", "SGD", "THB", "TWD", "USD", "ZAR"],
      )
    end

    it "skips null currency values" do
      records = provider.parse([
        {
          "end_of_day" => "1999-12-31",
          "usd" => 7.7915,
          "dem" => nil,
          "eur" => nil,
        },
      ])

      _(records.map { |r| r[:base] }).must_equal(["USD"])
    end

    it "skips records with missing end_of_day" do
      records = provider.parse([
        { "usd" => 7.8, "eur" => 9.2 },
        { "end_of_day" => "2026-02-28", "usd" => 7.8245 },
      ])

      _(records.length).must_equal(1)
    end
  end
end
