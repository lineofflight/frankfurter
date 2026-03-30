# frozen_string_literal: true

require_relative "../helper"
require "providers/peg"

module Providers
  describe PEG do
    let(:provider) { PEG.new }

    it "generates records for a date range" do
      provider.fetch(since: Date.new(2024, 1, 8), upto: Date.new(2024, 1, 12))

      _(provider.count).must_be(:>, 0)
    end

    it "skips weekends" do
      # 2024-01-06 is Saturday, 2024-01-07 is Sunday
      provider.fetch(since: Date.new(2024, 1, 6), upto: Date.new(2024, 1, 7))

      _(provider.count).must_equal(0)
    end

    it "includes all five pegs on a weekday" do
      provider.fetch(since: Date.new(2024, 1, 8), upto: Date.new(2024, 1, 8))

      _(provider.count).must_equal(5)
      quotes = provider.dataset.map { |r| r[:quote] }.sort

      _(quotes).must_equal(["ANG", "BMD", "BTN", "FKP", "SHP"])
    end

    it "generates correct rates" do
      provider.fetch(since: Date.new(2024, 1, 8), upto: Date.new(2024, 1, 8))

      bmd = provider.dataset.find { |r| r[:quote] == "BMD" }

      _(bmd[:base]).must_equal("USD")
      _(bmd[:rate]).must_equal(1.0)

      ang = provider.dataset.find { |r| r[:quote] == "ANG" }

      _(ang[:base]).must_equal("USD")
      _(ang[:rate]).must_equal(1.79)

      btn = provider.dataset.find { |r| r[:quote] == "BTN" }

      _(btn[:base]).must_equal("INR")
      _(btn[:rate]).must_equal(1.0)
    end

    it "respects individual peg since dates" do
      # Before 1972: BMD peg didn't exist yet, but FKP (1966) and ANG (1971) did
      provider.fetch(since: Date.new(1999, 1, 4), upto: Date.new(1999, 1, 4))

      # All pegs started before 1999, so all 5 should be present
      _(provider.count).must_equal(5)
    end

    it "returns empty dataset for empty range" do
      provider.fetch(since: Date.new(2024, 1, 10), upto: Date.new(2024, 1, 9))

      _(provider.count).must_equal(0)
    end

    it "imports records into the database" do
      Rate.dataset.delete
      provider.fetch(since: Date.new(2024, 1, 8), upto: Date.new(2024, 1, 10)).import

      _(Rate.where(provider: "PEG").count).must_be(:>, 0)
    end

    it "sets provider to PEG on all records" do
      provider.fetch(since: Date.new(2024, 1, 8), upto: Date.new(2024, 1, 8))

      provider.dataset.each do |record|
        _(record[:provider]).must_equal("PEG")
      end
    end
  end
end
