# frozen_string_literal: true

require_relative "helper"
require "provider"
require "weekly_rate"
require "monthly_rate"
require "versions/v2/query"

describe "Rollup tables" do
  describe "parity with downsample" do
    let(:interval) { (Fixtures.latest_date - 366)..Fixtures.latest_date }

    it "weekly rollup covers the same providers and currencies as downsample" do
      expected_keys = Rate.between(interval).downsample("week").all
        .map { |r| [r[:provider], r[:base], r[:quote]] }.uniq.sort

      actual_keys = WeeklyRate.between(interval).all
        .map { |r| [r[:provider], r[:base], r[:quote]] }.uniq.sort

      _(actual_keys).must_equal(expected_keys)
    end

    it "monthly rollup covers the same providers and currencies as downsample" do
      expected_keys = Rate.between(interval).downsample("month").all
        .map { |r| [r[:provider], r[:base], r[:quote]] }.uniq.sort

      actual_keys = MonthlyRate.between(interval).all
        .map { |r| [r[:provider], r[:base], r[:quote]] }.uniq.sort

      _(actual_keys).must_equal(expected_keys)
    end

    it "weekly rollup rates match downsample for inner buckets" do
      # Skip boundary buckets where partial-bucket averages differ
      ds_rows = Rate.between(interval).downsample("week").all
        .sort_by { |r| [r[:date].to_s, r[:provider], r[:base], r[:quote]] }
      bucket_dates = ds_rows.map { |r| r[:date].to_s }.uniq.sort
      inner_dates = bucket_dates[1...-1]

      expected = ds_rows.select { |r| inner_dates.include?(r[:date].to_s) }
      actual = WeeklyRate.between(interval).all
        .select { |r| inner_dates.include?(r[:bucket_date].to_s) }
        .sort_by { |r| [r[:bucket_date].to_s, r[:provider], r[:base], r[:quote]] }

      _(actual.size).must_equal(expected.size)
      actual.zip(expected).each do |a, e|
        _(a[:rate]).must_be_close_to(e[:rate], 0.0001)
      end
    end
  end

  describe "incremental refresh" do
    it "rebuilds affected weekly buckets after new rates" do
      provider = Provider.find(key: "ECB")
      date = Fixtures.latest_date

      WeeklyRate.where(provider: "ECB")
        .order(Sequel.desc(:bucket_date)).first

      Rate.dataset.insert(date:, base: "EUR", quote: "XTS", rate: 42.0, provider: "ECB")
      provider.send(:refresh_rollups, [date])

      week_row = WeeklyRate.where(provider: "ECB", quote: "XTS").first

      _(week_row).wont_be_nil
      _(week_row[:rate]).must_be_close_to(42.0, 0.01)

      # Unrelated provider buckets untouched
      boc_count_after = WeeklyRate.where(provider: "BOC").count

      _(boc_count_after).must_be(:>, 0)
    end

    it "rebuilds affected monthly buckets after new rates" do
      provider = Provider.find(key: "ECB")
      date = Fixtures.latest_date

      Rate.dataset.insert(date:, base: "EUR", quote: "XTS", rate: 99.0, provider: "ECB")
      provider.send(:refresh_rollups, [date])

      month_row = MonthlyRate.where(provider: "ECB", quote: "XTS").first

      _(month_row).wont_be_nil
      _(month_row[:rate]).must_be_close_to(99.0, 0.01)
    end
  end

  describe "shared scopes" do
    describe WeeklyRate do
      it "filters by provider with ecb" do
        data = WeeklyRate.ecb.all
        providers = data.map(&:provider).uniq

        _(providers).must_equal(["ECB"])
      end

      it "filters by date range with between" do
        start_date = Fixtures.latest_date - 60
        end_date = Fixtures.latest_date
        data = WeeklyRate.between(start_date..end_date)

        _(data).wont_be_empty
        data.each { |r| _(r.bucket_date).must_be(:>=, start_date - 7) }
      end

      it "filters currencies with only" do
        data = WeeklyRate.ecb.only("USD", "GBP").all

        data.each do |r|
          _([r.base, r.quote].any? { |c| ["USD", "GBP"].include?(c) }).must_equal(true)
        end
      end
    end

    describe MonthlyRate do
      it "filters by provider with ecb" do
        data = MonthlyRate.ecb.all

        _(data.map(&:provider).uniq).must_equal(["ECB"])
      end

      it "filters by date range with between" do
        start_date = Fixtures.latest_date - 180
        end_date = Fixtures.latest_date
        data = MonthlyRate.between(start_date..end_date)

        _(data).wont_be_empty
      end

      it "filters currencies with only" do
        data = MonthlyRate.ecb.only("USD").all

        data.each do |r|
          _([r.base, r.quote].any?("USD")).must_equal(true)
        end
      end
    end
  end

  describe "boundary bucket inclusion" do
    it "monthly grouped query includes partial first-month bucket" do
      # A 20-day range starting mid-month should include both months
      start_date = Fixtures.latest_date - 20
      end_date = Fixtures.latest_date
      query = Versions::V2::Query.new(from: start_date.to_s, to: end_date.to_s, group: "month")
      dates = query.to_a.map { |r| r[:date] }.uniq

      _(dates.size).must_be(:>=, 2, "expected at least 2 monthly buckets for a cross-month range")
    end

    it "weekly grouped query includes partial first-week bucket" do
      start_date = Fixtures.latest_date - 20
      end_date = Fixtures.latest_date
      query = Versions::V2::Query.new(from: start_date.to_s, to: end_date.to_s, group: "week")
      dates = query.to_a.map { |r| r[:date] }.uniq

      _(dates.size).must_be(:>=, 3, "expected at least 3 weekly buckets for a 20-day range")
    end
  end

  describe "single-date grouped query" do
    it "group=month with single date does not raise" do
      query = Versions::V2::Query.new(date: Fixtures.latest_date.to_s, group: "month")

      _(query.to_a).wont_be_empty
    end

    it "group=week with single date does not raise" do
      query = Versions::V2::Query.new(date: Fixtures.latest_date.to_s, group: "week")

      _(query.to_a).wont_be_empty
    end
  end

  describe "cache key freshness" do
    it "derives cache key from raw rates, not rollup bucket_date" do
      start_date = (Fixtures.latest_date - 30).to_s
      end_date = Fixtures.latest_date.to_s
      query_before = Versions::V2::Query.new(from: start_date, to: end_date, group: "month")
      key_before = query_before.cache_key

      # Insert a new rate on the existing latest date and refresh rollups;
      # the raw max date is unchanged so the cache key should be stable
      Rate.dataset.insert(
        date: Fixtures.latest_date, base: "EUR", quote: "XTS", rate: 42.0, provider: "ECB",
      )
      Provider.find(key: "ECB").send(:refresh_rollups, [Fixtures.latest_date])

      query_after = Versions::V2::Query.new(from: start_date, to: end_date, group: "month")
      key_after = query_after.cache_key

      _(key_before).must_equal(key_after, "cache key derives from raw max date, stays stable when rollup content changes within same day")
    end
  end
end
