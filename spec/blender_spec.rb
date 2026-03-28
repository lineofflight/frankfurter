# frozen_string_literal: true

require_relative "helper"
require "blender"

describe Blender do
  let(:date) { Date.parse("2024-01-15") }

  it "averages rates across providers" do
    rates = [
      { date: date, base: "EUR", quote: "USD", rate: 1.08, provider: "ECB" },
      { date: date, base: "EUR", quote: "GBP", rate: 0.84, provider: "ECB" },
      { date: date, base: "EUR", quote: "USD", rate: 1.10, provider: "BOC" },
      { date: date, base: "EUR", quote: "GBP", rate: 0.86, provider: "BOC" },
    ]

    result = Blender.new(rates, base: "EUR").blend
    usd = result.find { |r| r[:quote] == "USD" }

    _(usd[:rate]).must_be_close_to((1.08 + 1.10) / 2.0)
  end

  it "handles mixed bases within a provider" do
    rates = [
      { date: date, base: "USD", quote: "JPY", rate: 150.0, provider: "FRED" },
      { date: date, base: "USD", quote: "CHF", rate: 0.88, provider: "FRED" },
      { date: date, base: "EUR", quote: "USD", rate: 1.10, provider: "FRED" },
    ]

    result = Blender.new(rates, base: "USD").blend
    jpy = result.find { |r| r[:quote] == "JPY" }
    eur = result.find { |r| r[:quote] == "EUR" }

    _(jpy[:rate]).must_be_close_to(150.0)
    _(eur[:rate]).must_be_close_to(1.0 / 1.10)
  end

  it "blends rates consistently across providers with different bases" do
    rates = [
      { date: date, base: "EUR", quote: "USD", rate: 1.08, provider: "ECB" },
      { date: date, base: "USD", quote: "CAD", rate: 1.37, provider: "BOC" },
      { date: date, base: "EUR", quote: "CAD", rate: 1.48, provider: "BOC" },
    ]

    result = Blender.new(rates, base: "EUR").blend
    usd = result.find { |r| r[:quote] == "USD" }

    _(usd[:rate]).must_be_close_to(1.08, 0.05)
  end

  it "blends rates equally within the grace period" do
    rates = [
      { date: date, base: "EUR", quote: "USD", rate: 1.08, provider: "ECB" },
      { date: date - 1, base: "EUR", quote: "USD", rate: 1.10, provider: "BOC" },
    ]

    result = Blender.new(rates, base: "EUR").blend
    usd = result.find { |r| r[:quote] == "USD" }

    _(usd[:rate]).must_be_close_to((1.08 + 1.10) / 2.0)
    _(usd[:date]).must_equal(date)
  end

  it "discounts stale rates beyond the grace period" do
    rates = [
      { date: date, base: "EUR", quote: "USD", rate: 1.08, provider: "ECB" },
      { date: date - 7, base: "EUR", quote: "USD", rate: 1.20, provider: "BOC" },
    ]

    result = Blender.new(rates, base: "EUR").blend
    usd = result.find { |r| r[:quote] == "USD" }

    # 7-day-old rate has weight ~0.135 vs 1.0 for today's rate, so result skews toward 1.08
    _(usd[:rate]).must_be_close_to(1.08, 0.02)
    _(usd[:date]).must_equal(date)
  end
end
