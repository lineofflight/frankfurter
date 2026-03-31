# frozen_string_literal: true

require_relative "helper"
require "peg"

describe Peg do
  it "loads all pegs" do
    _(Peg.all).wont_be_empty
    _(Peg.all.length).must_equal(8)
  end

  it "returns frozen array" do
    _(Peg.all).must_be(:frozen?)
  end

  it "has correct attributes" do
    bmd = Peg.all.find { |p| p.quote == "BMD" }

    _(bmd.base).must_equal("USD")
    _(bmd.rate).must_equal(1.0)
    _(bmd.since).must_equal(Date.new(1972, 2, 6))
    _(bmd.authority).must_equal("Bermuda Monetary Authority")
    _(bmd.source).must_include("wikipedia")
  end

  it "finds peg by quote currency" do
    peg = Peg.find("BMD")

    _(peg).wont_be_nil
    _(peg.base).must_equal("USD")
  end

  it "returns nil for non-pegged currency" do
    _(Peg.find("EUR")).must_be_nil
  end

  it "includes non-1:1 peg rate" do
    ang = Peg.all.find { |p| p.quote == "ANG" }

    _(ang.rate).must_equal(1.79)
  end
end
