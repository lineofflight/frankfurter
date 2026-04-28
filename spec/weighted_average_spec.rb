# frozen_string_literal: true

require_relative "helper"
require "weighted_average"

describe WeightedAverage do
  let(:date) { Date.parse("2024-01-15") }

  it "averages rates from multiple providers" do
    rates = [
      { date: date, base: "EUR", quote: "USD", rate: 1.08, provider: "ECB" },
      { date: date, base: "EUR", quote: "USD", rate: 1.10, provider: "BOC" },
    ]

    result = WeightedAverage.new(rates).calculate
    usd = result.find { |r| r[:quote] == "USD" }

    _(usd[:rate]).must_be_close_to((1.08 + 1.10) / 2.0)
  end

  it "weights rates equally within the grace period" do
    rates = [
      { date: date, base: "EUR", quote: "USD", rate: 1.08, provider: "ECB" },
      { date: date - 1, base: "EUR", quote: "USD", rate: 1.10, provider: "BOC" },
    ]

    result = WeightedAverage.new(rates).calculate
    usd = result.find { |r| r[:quote] == "USD" }

    _(usd[:rate]).must_be_close_to((1.08 + 1.10) / 2.0)
  end

  it "picks the most recent date" do
    rates = [
      { date: date - 1, base: "EUR", quote: "USD", rate: 1.10, provider: "BOC" },
      { date: date, base: "EUR", quote: "USD", rate: 1.08, provider: "ECB" },
    ]

    result = WeightedAverage.new(rates).calculate
    usd = result.find { |r| r[:quote] == "USD" }

    _(usd[:date]).must_equal(date)
  end

  it "discounts stale rates beyond the grace period" do
    rates = [
      { date: date, base: "EUR", quote: "USD", rate: 1.08, provider: "ECB" },
      { date: date - 7, base: "EUR", quote: "USD", rate: 1.20, provider: "BOC" },
    ]

    result = WeightedAverage.new(rates).calculate
    usd = result.find { |r| r[:quote] == "USD" }

    _(usd[:rate]).must_be_close_to(1.08, 0.02)
  end

  it "exposes contributing providers per output row, sorted and unique" do
    rates = [
      { date: date, base: "EUR", quote: "USD", rate: 1.08, provider: "ECB" },
      { date: date, base: "EUR", quote: "USD", rate: 1.10, provider: "BOC" },
      { date: date - 1, base: "EUR", quote: "USD", rate: 1.09, provider: "BOC" },
    ]

    result = WeightedAverage.new(rates).calculate
    usd = result.find { |r| r[:quote] == "USD" }

    _(usd[:providers]).must_equal(["BOC", "ECB"])
  end
end
