# frozen_string_literal: true

require_relative "../../helper"
require "versions/v2/query"

module Versions
  describe V2::Query do
    it "raises on invalid date" do
      _ { V2::Query.new(date: "not-a-date") }.must_raise(V2::Query::ValidationError)
    end

    it "raises on conflicting date params" do
      _ { V2::Query.new(date: "2024-01-15", from: "2024-01-01") }.must_raise(V2::Query::ValidationError)
    end

    it "raises on invalid group" do
      _ { V2::Query.new(group: "day") }.must_raise(V2::Query::ValidationError)
    end

    it "accepts valid group" do
      query = V2::Query.new(from: "2024-01-01", to: "2024-03-31", group: "month")

      _(query.to_a).wont_be_empty
    end

    it "filters by quotes" do
      query = V2::Query.new(quotes: "USD,GBP")
      quotes = query.to_a.map { |r| r[:quote] }.uniq.sort

      _(quotes).must_equal(["GBP", "USD"])
    end

    it "returns empty array for dates before dataset" do
      query = V2::Query.new(date: "1901-01-01")

      _(query.to_a).must_be_empty
    end

    it "raises on invalid base currency" do
      _ { V2::Query.new(base: "FOO") }.must_raise(V2::Query::ValidationError)
    end

    it "raises on invalid quote currency" do
      _ { V2::Query.new(quotes: "USD,FOO") }.must_raise(V2::Query::ValidationError)
    end

    it "raises on invalid base and quotes together" do
      error = _ { V2::Query.new(base: "FOO", quotes: "BAR") }.must_raise(V2::Query::ValidationError)
      _(error.message).must_include("FOO")
      _(error.message).must_include("BAR")
    end

    it "snaps to nearest business day" do
      query = V2::Query.new(date: "2024-01-14") # Sunday

      _(query.to_a.first[:date]).must_equal("2024-01-12") # Friday
    end
  end
end
