# frozen_string_literal: true

require_relative "../../helper"
require "versions/v2/query"

module Versions
  describe V2::Query do
    it "raises on invalid date" do
      _ { V2::Query.new(date: "not-a-date") }.must_raise(V2::Query::ValidationError)
    end

    it "raises on conflicting date params" do
      date = Fixtures.latest_date.to_s

      _ { V2::Query.new(date:, from: (Fixtures.latest_date - 30).to_s) }.must_raise(V2::Query::ValidationError)
    end

    it "raises on invalid group" do
      _ { V2::Query.new(group: "day") }.must_raise(V2::Query::ValidationError)
    end

    it "accepts valid group" do
      range_start = (Fixtures.latest_date - 90).to_s
      range_end = Fixtures.latest_date.to_s
      query = V2::Query.new(from: range_start, to: range_end, group: "month")

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
      sunday = Fixtures.recent_sunday
      friday = Fixtures.preceding_friday(sunday)
      query = V2::Query.new(date: sunday.to_s)

      _(query.to_a.first[:date]).must_equal(friday.to_s)
    end
  end
end
