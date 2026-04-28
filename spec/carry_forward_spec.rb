# frozen_string_literal: true

require_relative "helper"
require "carry_forward"

describe CarryForward do
  describe ".apply" do
    it "returns the most recent rate per provider/base/quote" do
      rows = [
        { date: Date.new(2024, 1, 5), provider: "ECB", base: "EUR", quote: "USD", rate: 1.08 },
        { date: Date.new(2024, 1, 4), provider: "ECB", base: "EUR", quote: "USD", rate: 1.07 },
        { date: Date.new(2024, 1, 5), provider: "BOC", base: "CAD", quote: "USD", rate: 0.74 },
      ]

      result = CarryForward.apply(rows, date: Date.new(2024, 1, 6))

      _(result.size).must_equal(2)
      ecb = result.find { |r| r[:provider] == "ECB" }

      _(ecb[:date]).must_equal(Date.new(2024, 1, 5))
      _(ecb[:rate]).must_equal(1.08)
    end

    it "excludes rates outside the lookback window" do
      rows = [
        { date: Date.new(2024, 1, 1), provider: "ECB", base: "EUR", quote: "USD", rate: 1.08 },
      ]

      result = CarryForward.apply(rows, date: Date.new(2024, 1, 20), lookback: 14)

      _(result).must_be_empty
    end

    it "includes rates exactly at the lookback boundary" do
      rows = [
        { date: Date.new(2024, 1, 1), provider: "ECB", base: "EUR", quote: "USD", rate: 1.08 },
      ]

      result = CarryForward.apply(rows, date: Date.new(2024, 1, 15), lookback: 14)

      _(result.size).must_equal(1)
    end

    it "excludes rates after the target date" do
      rows = [
        { date: Date.new(2024, 1, 10), provider: "ECB", base: "EUR", quote: "USD", rate: 1.08 },
      ]

      result = CarryForward.apply(rows, date: Date.new(2024, 1, 9))

      _(result).must_be_empty
    end

    it "handles multiple currencies from the same provider" do
      rows = [
        { date: Date.new(2024, 1, 5), provider: "ECB", base: "EUR", quote: "USD", rate: 1.08 },
        { date: Date.new(2024, 1, 3), provider: "ECB", base: "EUR", quote: "GBP", rate: 0.86 },
      ]

      result = CarryForward.apply(rows, date: Date.new(2024, 1, 6))

      _(result.size).must_equal(2)
      _(result.map { |r| r[:quote] }.sort).must_equal(["GBP", "USD"])
    end

    it "returns empty array for empty input" do
      _(CarryForward.apply([], date: Date.new(2024, 1, 6))).must_be_empty
    end
  end
end
