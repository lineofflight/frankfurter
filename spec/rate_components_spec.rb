# frozen_string_literal: true

require_relative "helper"
require "rate_components"

describe RateComponents do
  let(:identity) { { provider: "TST", date: Date.new(2026, 9, 1), base: "USD", quote: "GBP" } }

  def stored(**components)
    Rate.dataset.insert(identity.merge(components))
    Rate.where(identity).first
  end

  it "exposes rate as a virtual column without storing a fourth price" do
    schema = DB.fetch("PRAGMA table_xinfo(rates)").all

    _(schema.find { |column| column[:name] == "rate" }[:hidden]).must_equal(2)
  end

  it "prefers a published mid even when the sides have a different midpoint" do
    _(stored(mid: 100, bid: 99, ask: 103)[:rate]).must_equal(100)
  end

  it "reproduces the decimal midpoint rather than binary addition noise" do
    _(stored(bid: 181.5264, ask: 181.76)[:rate]).must_equal(181.6432)
  end

  it "normalizes midpoint precision in SQL via printf" do
    _(stored(bid: 1830.59054685, ask: 1831.5063)[:rate]).must_equal(1831.04842343)
  end

  it "returns no effective rate for a single side" do
    _(stored(ask: 103)[:rate]).must_be_nil
  end

  it "keeps BOJA's existing zero-buy convention without inventing a published mid" do
    Rate.dataset.insert(identity.merge(provider: "BOJA", bid: 0, ask: 103))
    record = Rate.where(identity.merge(provider: "BOJA")).first

    _(record[:rate]).must_equal(103)
    _(record[:mid]).must_be_nil
  end

  it "retains a legacy adapter's reference and normalizes its precision" do
    attributes = RateComponents.attributes(identity.merge(rate: 0.9136230000000001))

    _(attributes[:mid]).must_equal(0.913623)
    _(attributes).wont_include(:rate)
  end

  it "keeps a derived midpoint out of the published mid field" do
    attributes = RateComponents.attributes(identity.merge(rate: 101, mid: nil, bid: 99, ask: 103))

    _(attributes[:mid]).must_be_nil
    _(attributes.values_at(:bid, :ask)).must_equal([99, 103])
  end
end
