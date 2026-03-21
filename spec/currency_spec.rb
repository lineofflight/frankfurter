# frozen_string_literal: true

require_relative "helper"
require "currency"

describe Currency do
  before do
    Rate.dataset.delete
    Rate.multi_insert([
      { provider: "ECB", date: Date.today, base: "EUR", quote: "USD", rate: 1.1 },
      { provider: "ECB", date: Date.today, base: "EUR", quote: "GBP", rate: 0.85 },
      { provider: "BOC", date: Date.today, base: "CAD", quote: "USD", rate: 0.74 },
      { provider: "ECB", date: Date.today - 365, base: "EUR", quote: "SEK", rate: 11.0 },
    ])
  end

  it "lists all currencies" do
    codes = Currency.all.map(&:iso_code).sort

    _(codes).must_include("USD")
    _(codes).must_include("EUR")
    _(codes).must_include("CAD")
  end

  it "merges date ranges across quote and base" do
    usd = Currency.find("USD")

    _(usd).wont_be_nil
    _(usd.start_date.to_s).must_equal(Date.today.to_s)
    _(usd.end_date.to_s).must_equal(Date.today.to_s)
  end

  it "includes base currencies" do
    eur = Currency.find("EUR")

    _(eur).wont_be_nil
  end

  it "filters active currencies" do
    active_codes = Currency.active.map(&:iso_code)

    _(active_codes).must_include("USD")
    _(active_codes).wont_include("SEK")
  end

  it "returns nil for unknown currency" do
    _(Currency.find("XYZ")).must_be_nil
  end

  it "formats to hash" do
    usd = Currency.find("USD")

    _(usd.to_h[:name]).must_equal("United States Dollar")
    _(usd.to_h[:symbol]).must_equal("$")
    _(usd.to_h[:iso_numeric]).must_equal("840")
  end

  it "includes providers" do
    usd = Currency.find("USD")

    _(usd.providers).must_include("ECB")
    _(usd.providers).must_include("BOC")
  end

  it "is case insensitive" do
    _(Currency.find("usd")).wont_be_nil
  end
end
