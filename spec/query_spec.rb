# frozen_string_literal: true

require_relative "helper"
require "query"

describe Query do
  it "builds a query hash" do
    _(Query.build(date: "2014-01-01")).must_be_kind_of(Hash)
  end

  it "returns given amount" do
    query = Query.new(amount: "100")

    _(query.amount).must_equal(100.0)
  end

  it "requires a positive amount" do
    _ { Query.new(amount: "0").to_h }.must_raise(ArgumentError)
    _ { Query.new(amount: "-1").to_h }.must_raise(ArgumentError)
  end

  it "defaults amount to nothing" do
    query = Query.new

    _(query.amount).must_be_nil
  end

  it "returns given base" do
    query = Query.new(base: "USD")

    _(query.base).must_equal("USD")
  end

  it "upcases given base" do
    query = Query.new(base: "usd")

    _(query.base).must_equal("USD")
  end

  it "defaults base to nothing" do
    query = Query.new

    _(query.base).must_be_nil
  end

  it "aliases base with from" do
    query = Query.new(from: "USD")

    _(query.base).must_equal("USD")
  end

  it "returns given symbols" do
    query = Query.new(symbols: "USD,GBP")

    _(query.symbols).must_equal(["USD", "GBP"])
  end

  it "upcases given symbols" do
    query = Query.new(symbols: "usd,gbp")

    _(query.symbols).must_equal(["USD", "GBP"])
  end

  it "aliases symbols with to" do
    query = Query.new(to: "USD")

    _(query.symbols).must_equal(["USD"])
  end

  it "defaults symbols to nothing" do
    query = Query.new

    _(query.symbols).must_be_nil
  end

  it "returns given date" do
    date = "2014-01-01"
    query = Query.new(date:)

    _(query.date).must_equal(Date.parse(date))
  end

  it "requires a valid date" do
    _ { Query.new(date: "2014-01-32").to_h }.must_raise(Date::Error)
  end

  it "returns given date interval" do
    start_date = "2014-01-01"
    end_date = "2014-12-31"
    query = Query.new(start_date:, end_date:)

    _(query.date).must_equal(Date.parse(start_date)..Date.parse(end_date))
  end
end
