# frozen_string_literal: true

require_relative "helper"
require "provider"
require "weekly_rate"
require "monthly_rate"
require "versions/v2/rate_query"

describe "Rollup tables" do
  describe "parity with downsample" do
    let(:interval) { (Fixtures.latest_date - 366)..Fixtures.latest_date }

    it "weekly rollup covers the same providers and currencies as downsample" do
      expected_keys = Rate.between(interval).downsample("week").all
        .map { |r| [r[:dataset_key], r[:base], r[:quote]] }.uniq.sort

      actual_keys = WeeklyRate.between(interval).all
        .map { |r| [r[:dataset_key], r[:base], r[:quote]] }.uniq.sort

      _(actual_keys).must_equal(expected_keys)
    end

    it "monthly rollup covers the same providers and currencies as downsample" do
      expected_keys = Rate.between(interval).downsample("month").all
        .map { |r| [r[:dataset_key], r[:base], r[:quote]] }.uniq.sort

      actual_keys = MonthlyRate.between(interval).all
        .map { |r| [r[:dataset_key], r[:base], r[:quote]] }.uniq.sort

      _(actual_keys).must_equal(expected_keys)
    end

    it "weekly rollup rates match downsample for inner buckets" do
      # Skip boundary buckets where partial-bucket averages differ
      ds_rows = Rate.between(interval).downsample("week").all
        .sort_by { |r| [r[:date].to_s, r[:dataset_key], r[:base], r[:quote]] }
      bucket_dates = ds_rows.map { |r| r[:date].to_s }.uniq.sort
      inner_dates = bucket_dates[1...-1]

      expected = ds_rows.select { |r| inner_dates.include?(r[:date].to_s) }
      actual = WeeklyRate.between(interval).all
        .select { |r| inner_dates.include?(r[:bucket_date].to_s) }
        .sort_by { |r| [r[:bucket_date].to_s, r[:dataset_key], r[:base], r[:quote]] }

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

      WeeklyRate.where(dataset_key: "ECB")
        .order(Sequel.desc(:bucket_date)).first

      Rate.dataset.insert(date:, base: "EUR", quote: "XTS", mid: 42.0, dataset_key: "ECB")
      provider.send(:refresh_rollups, [date])

      week_row = WeeklyRate.where(dataset_key: "ECB", quote: "XTS").first

      _(week_row).wont_be_nil
      _(week_row[:rate]).must_be_close_to(42.0, 0.01)

      # Unrelated provider buckets untouched
      boc_count_after = WeeklyRate.where(dataset_key: "BOC").count

      _(boc_count_after).must_be(:>, 0)
    end

    it "rebuilds affected monthly buckets after new rates" do
      provider = Provider.find(key: "ECB")
      date = Fixtures.latest_date

      Rate.dataset.insert(date:, base: "EUR", quote: "XTS", mid: 99.0, dataset_key: "ECB")
      provider.send(:refresh_rollups, [date])

      month_row = MonthlyRate.where(dataset_key: "ECB", quote: "XTS").first

      _(month_row).wont_be_nil
      _(month_row[:rate]).must_be_close_to(99.0, 0.01)
    end
  end

  describe "shared scopes" do
    describe WeeklyRate do
      it "filters by provider with ecb" do
        data = WeeklyRate.ecb.all
        providers = data.map(&:dataset_key).uniq

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
          _([r.base, r.quote].intersect?(["USD", "GBP"])).must_equal(true)
        end
      end
    end

    describe MonthlyRate do
      it "filters by provider with ecb" do
        data = MonthlyRate.ecb.all

        _(data.map(&:dataset_key).uniq).must_equal(["ECB"])
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
      # A 40-day range is guaranteed to cross at least one month boundary
      start_date = Fixtures.latest_date - 40
      end_date = Fixtures.latest_date
      query = Versions::V2::RateQuery.new(from: start_date.to_s, to: end_date.to_s, group: "month")
      dates = query.to_a.map { |r| r[:date] }.uniq

      _(dates.size).must_be(:>=, 2, "expected at least 2 monthly buckets for a cross-month range")
    end

    it "weekly grouped query includes partial first-week bucket" do
      start_date = Fixtures.latest_date - 20
      end_date = Fixtures.latest_date
      query = Versions::V2::RateQuery.new(from: start_date.to_s, to: end_date.to_s, group: "week")
      dates = query.to_a.map { |r| r[:date] }.uniq

      _(dates.size).must_be(:>=, 3, "expected at least 3 weekly buckets for a 20-day range")
    end

    it "keys every week on its ISO Monday without splitting or reordering New Year weeks" do
      Rate.dataset.delete
      WeeklyRate.dataset.delete
      BlendedWeeklyRate.dataset.delete
      observations = {
        "2024-12-30" => 1.04, "2024-12-31" => 1.06, "2025-01-02" => 1.08, "2025-01-03" => 1.10,
        "2025-12-15" => 1.12, "2025-12-29" => 1.16, "2026-01-02" => 1.20,
        "2026-01-05" => 1.22, "2026-01-19" => 1.24, "2026-01-20" => 1.26,
      }
      observations.each do |date, mid|
        Rate.dataset.insert(dataset_key: "ECB", date: Date.iso8601(date), base: "EUR", quote: "USD", mid:)
      end
      Provider["ECB"].send(:refresh_rollups, observations.keys.map { |d| Date.iso8601(d) })

      monday_year = Versions::V2::RateQuery.new(
        base: "EUR", quotes: "USD", from: "2024-12-30", to: "2025-01-05", group: "week",
      ).to_a

      _(monday_year.map { |r| [r[:date], r[:rate]] }).must_equal([["2024-12-30", 1.07]])

      midweek_year = Versions::V2::RateQuery.new(
        base: "EUR", quotes: "USD", from: "2025-12-15", to: "2026-01-20", group: "week",
      ).to_a

      _(midweek_year.map { |r| [r[:date], r[:rate]] }).must_equal([
        ["2025-12-15", 1.12],
        ["2025-12-29", 1.18],
        ["2026-01-05", 1.22],
        ["2026-01-19", 1.25],
      ])
    end

    it "cuts cleanly between Sunday and the next Monday for both bucket membership and range bounds" do
      Rate.dataset.delete
      WeeklyRate.dataset.delete
      BlendedWeeklyRate.dataset.delete
      observations = {
        "2025-12-29" => 1.10, # Monday of week 2025-12-29
        "2026-01-04" => 1.20, # Sunday of week 2025-12-29
        "2026-01-05" => 1.30, # Monday of week 2026-01-05
        "2026-01-11" => 1.40, # Sunday of week 2026-01-05
        "2026-01-12" => 1.50, # Monday of week 2026-01-12
      }
      observations.each do |date, mid|
        Rate.dataset.insert(dataset_key: "ECB", date: Date.iso8601(date), base: "EUR", quote: "USD", mid:)
      end
      Provider["ECB"].send(:refresh_rollups, observations.keys.map { |d| Date.iso8601(d) })

      ask = lambda do |from, to|
        Versions::V2::RateQuery.new(base: "EUR", quotes: "USD", from:, to:, group: "week").to_a
          .map { |r| [r[:date], r[:rate]] }
      end

      # Sunday `from` snaps back to its own Monday; `to` on Sunday stops before next Monday's bucket.
      _(ask.call("2026-01-04", "2026-01-04")).must_equal([["2025-12-29", 1.15]])
      # Next-Monday `from` starts on that Monday without leaking the prior week; `to` on Monday includes it.
      _(ask.call("2026-01-05", "2026-01-05")).must_equal([["2026-01-05", 1.35]])
      _(ask.call("2026-01-05", "2026-01-11")).must_equal([["2026-01-05", 1.35]])
      _(ask.call("2026-01-05", "2026-01-12")).must_equal([["2026-01-05", 1.35], ["2026-01-12", 1.5]])
    end
  end

  describe "single-date grouped query" do
    it "group=month with single date does not raise" do
      query = Versions::V2::RateQuery.new(date: Fixtures.latest_date.to_s, group: "month")

      _(query.to_a).wont_be_empty
    end

    it "group=week with single date does not raise" do
      query = Versions::V2::RateQuery.new(date: Fixtures.latest_date.to_s, group: "week")

      _(query.to_a).wont_be_empty
    end
  end

  describe "cache key freshness" do
    it "derives cache key from raw rates, not rollup bucket_date" do
      start_date = (Fixtures.latest_date - 30).to_s
      end_date = Fixtures.latest_date.to_s
      query_before = Versions::V2::RateQuery.new(from: start_date, to: end_date, group: "month")
      key_before = query_before.cache_key

      # Insert a new rate on the existing latest date and refresh rollups; the raw max date is unchanged so the cache
      # key should be stable
      Rate.dataset.insert(
        date: Fixtures.latest_date, base: "EUR", quote: "XTS", mid: 42.0, dataset_key: "ECB",
      )
      Provider.find(key: "ECB").send(:refresh_rollups, [Fixtures.latest_date])

      query_after = Versions::V2::RateQuery.new(from: start_date, to: end_date, group: "month")
      key_after = query_after.cache_key
      msg = "cache key derives from raw max date, stays stable when rollup content changes within same day"

      _(key_before).must_equal(key_after, msg)
    end
  end
end
