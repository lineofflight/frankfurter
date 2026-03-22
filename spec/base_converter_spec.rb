# frozen_string_literal: true

require_relative "helper"
require "base_converter"

describe BaseConverter do
  let(:date) { Date.parse("2024-01-15") }
  let(:rates) do
    [
      { date: date, base: "EUR", quote: "USD", rate: 1.08, provider: "ECB" },
      { date: date, base: "EUR", quote: "GBP", rate: 0.85, provider: "ECB" },
    ]
  end

  it "converts rates to a different base" do
    result = BaseConverter.new(rates, base: "USD").convert

    _(result.length).must_equal(2)
    gbp = result.find { |r| r[:quote] == "GBP" }

    _(gbp[:rate]).must_be_close_to(0.85 / 1.08)
  end

  it "produces an inverse rate for the native base" do
    result = BaseConverter.new(rates, base: "USD").convert
    eur = result.find { |r| r[:quote] == "EUR" }

    _(eur[:rate]).must_be_close_to(1.0 / 1.08)
  end

  it "uses rates as-is when native base matches requested base" do
    result = BaseConverter.new(rates, base: "EUR").convert
    usd = result.find { |r| r[:quote] == "USD" }

    _(usd[:rate]).must_equal(1.08)
  end

  it "returns empty when base currency is not available" do
    result = BaseConverter.new(rates, base: "JPY").convert

    _(result).must_be_empty
  end

  it "cross-converts through a shared quote currency" do
    rates = [
      { date: date, base: "USD", quote: "CAD", rate: 1.37, provider: "BOC" },
      { date: date, base: "EUR", quote: "CAD", rate: 1.48, provider: "BOC" },
      { date: date, base: "TRY", quote: "CAD", rate: 0.031, provider: "BOC" },
    ]

    result = BaseConverter.new(rates, base: "EUR").convert
    usd = result.find { |r| r[:quote] == "USD" }
    try = result.find { |r| r[:quote] == "TRY" }

    _(usd[:rate]).must_be_close_to(1.48 / 1.37)
    _(try[:rate]).must_be_close_to(1.48 / 0.031)
  end

  it "handles mixed bases by finding the target as a base in inverted rows" do
    mixed = [
      { date: date, base: "USD", quote: "JPY", rate: 150.0, provider: "FRED" },
      { date: date, base: "EUR", quote: "USD", rate: 1.10, provider: "FRED" },
    ]

    result = BaseConverter.new(mixed, base: "EUR").convert
    jpy = result.find { |r| r[:quote] == "JPY" }
    usd = result.find { |r| r[:quote] == "USD" }

    _(jpy[:rate]).must_be_close_to(150.0 * 1.10)
    _(usd[:rate]).must_equal(1.10)
  end
end
