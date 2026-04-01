# frozen_string_literal: true

require_relative "helper"
require "consensus"

describe Consensus do
  def seed_providers(date, quote: "USD", rates:)
    rates.each do |provider, rate|
      Rate.unfiltered.insert(
        date:, provider:, base: "EUR", quote:, rate:,
      )
    end
  end

  it "flags a rate that deviates from consensus" do
    seed_providers(Date.today, rates: {
      "A" => 1.10, "B" => 1.11, "C" => 1.10, "D" => 9.99,
    })

    Consensus.flag(Date.today)

    _(Rate.unfiltered.where(provider: "D", date: Date.today).first[:outlier]).must_equal(true)
    _(Rate.unfiltered.where(provider: "A", date: Date.today).first[:outlier]).must_equal(false)
  end

  it "does not flag rates within consensus" do
    seed_providers(Date.today, rates: {
      "A" => 1.10, "B" => 1.11, "C" => 1.12,
    })

    result = Consensus.flag(Date.today)

    _(result.outliers).must_be_empty
  end

  it "skips quotes with fewer than 3 providers" do
    seed_providers(Date.today, quote: "XYZ", rates: { "A" => 1.10, "B" => 9.99 })

    Consensus.flag(Date.today)

    _(Rate.unfiltered.where(provider: "B", date: Date.today, quote: "XYZ").first[:outlier]).must_equal(false)
  end

  it "flags outliers even when history contains existing extreme values" do
    seed_providers(Date.today, rates: {
      "A" => 1.10, "B" => 1.11, "C" => 1.10, "D" => 999.0,
    })

    Consensus.flag(Date.today)

    _(Rate.unfiltered.where(provider: "D", date: Date.today).first[:outlier]).must_equal(true)
  end

  it "tolerates small fluctuations in low-volatility pairs" do
    seed_providers(Date.today, rates: {
      "A" => 612.55, "B" => 612.50, "C" => 612.60, "D" => 610.40,
    })

    Consensus.flag(Date.today)

    _(Rate.unfiltered.where(provider: "D", date: Date.today).first[:outlier]).must_equal(false)
  end

  it "does not update when using find" do
    seed_providers(Date.today, rates: {
      "A" => 1.10, "B" => 1.11, "C" => 1.10, "D" => 9.99,
    })

    result = Consensus.find(Date.today)

    _(result.outliers).wont_be_empty
    _(Rate.unfiltered.where(provider: "D", date: Date.today).first[:outlier]).must_equal(false)
  end

  it "returns outliers" do
    seed_providers(Date.today, rates: {
      "A" => 1.10, "B" => 1.11, "C" => 1.10, "D" => 9.99,
    })

    result = Consensus.find(Date.today)

    _(result.outliers.size).must_equal(1)
  end

  it "unflags rates that return to consensus" do
    seed_providers(Date.today, rates: {
      "A" => 1.10, "B" => 1.11, "C" => 1.10, "D" => 9.99,
    })
    Consensus.flag(Date.today)

    _(Rate.unfiltered.where(provider: "D", date: Date.today).first[:outlier]).must_equal(true)

    # Provider D corrects its rate
    Rate.unfiltered.where(provider: "D", date: Date.today).update(rate: 1.105, outlier: true)
    Consensus.flag(Date.today)

    _(Rate.unfiltered.where(provider: "D", date: Date.today).first[:outlier]).must_equal(false)
  end

  it "flags the BCEAO-style cross-rate error" do
    date = Date.today

    # Provider A: EUR/USD and EUR/AED
    Rate.unfiltered.insert(date:, provider: "A", base: "EUR", quote: "USD", rate: 1.17)
    Rate.unfiltered.insert(date:, provider: "A", base: "EUR", quote: "AED", rate: 4.30)

    # Provider B: EUR/USD and EUR/AED
    Rate.unfiltered.insert(date:, provider: "B", base: "EUR", quote: "USD", rate: 1.17)
    Rate.unfiltered.insert(date:, provider: "B", base: "EUR", quote: "AED", rate: 4.30)

    # Provider C: EUR/USD and EUR/AED (bad — off by 6x, like BCEAO)
    Rate.unfiltered.insert(date:, provider: "C", base: "EUR", quote: "USD", rate: 1.17)
    Rate.unfiltered.insert(date:, provider: "C", base: "EUR", quote: "AED", rate: 25.8)

    Consensus.flag(date)

    _(Rate.unfiltered.where(provider: "C", date:, quote: "AED").first[:outlier]).must_equal(true)
    _(Rate.unfiltered.where(provider: "A", date:, quote: "AED").first[:outlier]).must_equal(false)
  end
end
