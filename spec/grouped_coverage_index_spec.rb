# frozen_string_literal: true

require_relative "helper"
require "weekly_rate"

describe "weekly source coverage index" do
  it "covers provider exclusions in both range and snap-back lookups" do
    query = WeeklyRate.blendable.between((Date.today - 365)..Date.today)
      .select(:bucket_date).distinct.order(:bucket_date)
    searches = DB["EXPLAIN QUERY PLAN #{query.sql}"].all
      .map { |row| row[:detail] }.select { |detail| detail.start_with?("SEARCH") }

    _(searches.size).must_equal(2)
    searches.each { |detail| _(detail).must_include("USING COVERING INDEX") }
  end
end
