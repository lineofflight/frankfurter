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

  describe ".each_snapshot" do
    # each_snapshot is a sliding-window optimization of calling .apply once per anchor date.
    # It must yield, for each date, the identical contributor set that .apply produces — .apply
    # is the trusted oracle. Contributors are compared as sets (keyed by provider/base/quote)
    # because downstream blending is order-independent.
    def normalize(rows)
      rows.map { |r| [r[:provider], r[:base], r[:quote], r[:date], r[:rate]] }.sort
    end

    def assert_matches_apply(rows, dates, lookback: CarryForward::LOOKBACK_DAYS)
      collected = {}
      CarryForward.each_snapshot(rows, dates:, lookback:) { |date, contributors| collected[date] = contributors }

      _(collected.keys.sort).must_equal(dates.sort)
      dates.each do |date|
        expected = CarryForward.apply(rows, date:, lookback:)

        _(normalize(collected.fetch(date))).must_equal(normalize(expected))
      end
    end

    it "matches .apply for each anchor across a realistic multi-provider series" do
      rows = []
      base_dates = (1..40).map { |d| Date.new(2024, 1, 1) + d }
      base_dates.each_with_index do |date, i|
        next if date.saturday? || date.sunday? # weekend gaps, like production

        rows << { date:, provider: "ECB", base: "EUR", quote: "USD", rate: 1.08 + i * 0.001 }
        rows << { date:, provider: "ECB", base: "EUR", quote: "GBP", rate: 0.86 + i * 0.001 }
        rows << { date:, provider: "BOC", base: "CAD", quote: "USD", rate: 0.74 + i * 0.001 }
      end

      anchors = ((Date.new(2024, 1, 1))..(Date.new(2024, 2, 20))).to_a

      assert_matches_apply(rows, anchors)
    end

    it "matches .apply when rows fall out of the lookback window between anchors" do
      rows = [
        { date: Date.new(2024, 1, 1), provider: "ECB", base: "EUR", quote: "USD", rate: 1.08 },
        { date: Date.new(2024, 1, 2), provider: "BOC", base: "CAD", quote: "USD", rate: 0.74 },
        { date: Date.new(2024, 1, 30), provider: "ECB", base: "EUR", quote: "USD", rate: 1.09 },
      ]
      anchors = ((Date.new(2024, 1, 1))..(Date.new(2024, 2, 5))).to_a

      assert_matches_apply(rows, anchors)
    end

    it "matches .apply for anchors that precede all data" do
      rows = [{ date: Date.new(2024, 6, 1), provider: "ECB", base: "EUR", quote: "USD", rate: 1.08 }]
      anchors = [Date.new(2024, 1, 1), Date.new(2024, 5, 1), Date.new(2024, 6, 1)]

      assert_matches_apply(rows, anchors)
    end

    it "matches .apply with a custom lookback" do
      rows = [
        { date: Date.new(2024, 1, 1), provider: "ECB", base: "EUR", quote: "USD", rate: 1.08 },
        { date: Date.new(2024, 1, 5), provider: "ECB", base: "EUR", quote: "USD", rate: 1.09 },
      ]
      anchors = ((Date.new(2024, 1, 1))..(Date.new(2024, 1, 20))).to_a

      assert_matches_apply(rows, anchors, lookback: 3)
    end

    it "handles unsorted anchor dates" do
      rows = [{ date: Date.new(2024, 1, 10), provider: "ECB", base: "EUR", quote: "USD", rate: 1.08 }]

      assert_matches_apply(rows, [Date.new(2024, 1, 15), Date.new(2024, 1, 10), Date.new(2024, 1, 12)])
    end

    it "yields each date with empty contributors when rows are empty" do
      yielded = false
      CarryForward.each_snapshot([], dates: [Date.new(2024, 1, 1)]) { |_, _| yielded = true }

      _(yielded).must_equal(true)
    end
  end
end
