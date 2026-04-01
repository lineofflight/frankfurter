# frozen_string_literal: true

require_relative "helper"
require "consensus"

describe Consensus do
  def build_rates(quote: "USD", providers:)
    providers.map do |provider, rate|
      { date: Date.today, base: "EUR", quote:, rate:, provider: }
    end
  end

  it "filters a rate that deviates from consensus" do
    rates = build_rates(providers: { "A" => 1.10, "B" => 1.11, "C" => 1.10, "D" => 9.99 })
    consensus = Consensus.new(rates)
    result = consensus.find

    _(result.none? { |r| r[:provider] == "D" }).must_equal(true)
    _(consensus.outliers).must_include(["D", "USD"])
  end

  it "keeps all rates within consensus" do
    rates = build_rates(providers: { "A" => 1.10, "B" => 1.11, "C" => 1.12, "D" => 1.105 })
    consensus = Consensus.new(rates)

    _(consensus.find.size).must_equal(4)
    _(consensus.outliers).must_be_empty
  end

  it "skips quotes with fewer than 4 providers" do
    rates = build_rates(providers: { "A" => 1.10, "B" => 9.99, "C" => 1.11 })
    consensus = Consensus.new(rates)

    _(consensus.find.size).must_equal(3)
    _(consensus.outliers).must_be_empty
  end

  it "tolerates small fluctuations in low-volatility pairs" do
    rates = build_rates(providers: { "A" => 612.55, "B" => 612.50, "C" => 612.60, "D" => 610.40 })
    consensus = Consensus.new(rates)

    consensus.find

    _(consensus.outliers).must_be_empty
  end

  it "filters per quote, not per provider" do
    rates = [
      { date: Date.today, base: "EUR", quote: "USD", rate: 1.17, provider: "A" },
      { date: Date.today, base: "EUR", quote: "AED", rate: 4.30, provider: "A" },
      { date: Date.today, base: "EUR", quote: "USD", rate: 1.17, provider: "B" },
      { date: Date.today, base: "EUR", quote: "AED", rate: 4.30, provider: "B" },
      { date: Date.today, base: "EUR", quote: "USD", rate: 1.17, provider: "C" },
      { date: Date.today, base: "EUR", quote: "AED", rate: 25.8, provider: "C" },
      { date: Date.today, base: "EUR", quote: "USD", rate: 1.17, provider: "D" },
      { date: Date.today, base: "EUR", quote: "AED", rate: 4.30, provider: "D" },
    ]
    consensus = Consensus.new(rates)
    result = consensus.find

    _(consensus.outliers).must_include(["C", "AED"])
    _(consensus.outliers).wont_include(["C", "USD"])
    _(result.any? { |r| r[:provider] == "C" && r[:quote] == "USD" }).must_equal(true)
    _(result.none? { |r| r[:provider] == "C" && r[:quote] == "AED" }).must_equal(true)
  end
end
