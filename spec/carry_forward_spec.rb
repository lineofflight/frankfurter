# frozen_string_literal: true

require_relative "helper"
require "carry_forward"

describe CarryForward do
  describe ".latest" do
    it "returns the most recent rate per provider/base/quote" do
      rows = [
        { date: Date.new(2024, 1, 5), provider: "ECB", base: "EUR", quote: "USD", rate: 1.08 },
        { date: Date.new(2024, 1, 4), provider: "ECB", base: "EUR", quote: "USD", rate: 1.07 },
        { date: Date.new(2024, 1, 5), provider: "BOC", base: "CAD", quote: "USD", rate: 0.74 },
      ]

      result = CarryForward.latest(rows, date: Date.new(2024, 1, 6))

      _(result.size).must_equal(2)
      ecb = result.find { |r| r[:provider] == "ECB" }

      _(ecb[:date]).must_equal(Date.new(2024, 1, 5))
      _(ecb[:rate]).must_equal(1.08)
    end

    it "excludes rates outside the lookback window" do
      rows = [
        { date: Date.new(2024, 1, 1), provider: "ECB", base: "EUR", quote: "USD", rate: 1.08 },
      ]

      result = CarryForward.latest(rows, date: Date.new(2024, 1, 20), lookback: 14)

      _(result).must_be_empty
    end

    it "includes rates exactly at the lookback boundary" do
      rows = [
        { date: Date.new(2024, 1, 1), provider: "ECB", base: "EUR", quote: "USD", rate: 1.08 },
      ]

      result = CarryForward.latest(rows, date: Date.new(2024, 1, 15), lookback: 14)

      _(result.size).must_equal(1)
    end

    it "excludes rates after the target date" do
      rows = [
        { date: Date.new(2024, 1, 10), provider: "ECB", base: "EUR", quote: "USD", rate: 1.08 },
      ]

      result = CarryForward.latest(rows, date: Date.new(2024, 1, 9))

      _(result).must_be_empty
    end

    it "handles multiple currencies from the same provider" do
      rows = [
        { date: Date.new(2024, 1, 5), provider: "ECB", base: "EUR", quote: "USD", rate: 1.08 },
        { date: Date.new(2024, 1, 3), provider: "ECB", base: "EUR", quote: "GBP", rate: 0.86 },
      ]

      result = CarryForward.latest(rows, date: Date.new(2024, 1, 6))

      _(result.size).must_equal(2)
      _(result.map { |r| r[:quote] }.sort).must_equal(["GBP", "USD"])
    end

    it "returns empty array for empty input" do
      _(CarryForward.latest([], date: Date.new(2024, 1, 6))).must_be_empty
    end
  end

  describe ".enrich" do
    it "carries forward rates from prior days into dates within the range" do
      friday = Date.new(2024, 1, 5)
      saturday = Date.new(2024, 1, 6)

      rows = [
        { date: friday, provider: "ECB", base: "EUR", quote: "USD", rate: 1.08 },
        { date: friday, provider: "BOC", base: "CAD", quote: "USD", rate: 0.74 },
        { date: saturday, provider: "HNB", base: "EUR", quote: "USD", rate: 1.09 },
      ]

      result = CarryForward.enrich(rows, range: saturday..saturday)

      _(result.keys).must_equal([saturday])
      providers = result[saturday].map { |r| r[:provider] }.sort

      _(providers).must_equal(["BOC", "ECB", "HNB"])
    end

    it "preserves original dates on carried-forward rows" do
      friday = Date.new(2024, 1, 5)
      saturday = Date.new(2024, 1, 6)

      rows = [
        { date: friday, provider: "ECB", base: "EUR", quote: "USD", rate: 1.08 },
        { date: saturday, provider: "HNB", base: "EUR", quote: "USD", rate: 1.09 },
      ]

      result = CarryForward.enrich(rows, range: saturday..saturday)
      ecb = result[saturday].find { |r| r[:provider] == "ECB" }

      _(ecb[:date]).must_equal(friday)
    end

    it "excludes carry-forward beyond the lookback window" do
      old = Date.new(2024, 1, 1)
      target = Date.new(2024, 1, 8)

      rows = [
        { date: old, provider: "ECB", base: "EUR", quote: "USD", rate: 1.08 },
        { date: target, provider: "HNB", base: "EUR", quote: "USD", rate: 1.09 },
      ]

      result = CarryForward.enrich(rows, range: target..target, lookback: 5)
      providers = result[target].map { |r| r[:provider] }

      _(providers).must_include("HNB")
      _(providers).wont_include("ECB")
    end

    it "only returns dates within the target range" do
      friday = Date.new(2024, 1, 5)
      saturday = Date.new(2024, 1, 6)
      sunday = Date.new(2024, 1, 7)

      rows = [
        { date: friday, provider: "ECB", base: "EUR", quote: "USD", rate: 1.08 },
        { date: saturday, provider: "HNB", base: "EUR", quote: "USD", rate: 1.09 },
      ]

      result = CarryForward.enrich(rows, range: saturday..sunday)

      _(result.keys).must_equal([saturday])
      _(result).wont_include(friday)
    end

    it "picks the most recent rate per provider within the lookback" do
      wed = Date.new(2024, 1, 3)
      fri = Date.new(2024, 1, 5)
      sat = Date.new(2024, 1, 6)

      rows = [
        { date: wed, provider: "ECB", base: "EUR", quote: "USD", rate: 1.07 },
        { date: fri, provider: "ECB", base: "EUR", quote: "USD", rate: 1.08 },
        { date: sat, provider: "HNB", base: "EUR", quote: "USD", rate: 1.09 },
      ]

      result = CarryForward.enrich(rows, range: sat..sat)
      ecb = result[sat].find { |r| r[:provider] == "ECB" }

      _(ecb[:rate]).must_equal(1.08)
      _(ecb[:date]).must_equal(fri)
    end

    it "returns empty hash when no dates have data in range" do
      rows = [
        { date: Date.new(2024, 1, 1), provider: "ECB", base: "EUR", quote: "USD", rate: 1.08 },
      ]

      result = CarryForward.enrich(rows, range: Date.new(2024, 2, 1)..Date.new(2024, 2, 2))

      _(result).must_be_empty
    end
  end
end
