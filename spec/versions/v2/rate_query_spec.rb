# frozen_string_literal: true

require_relative "../../helper"
require "versions/v2/rate_query"

module Versions
  describe V2::RateQuery do
    it "raises on invalid date" do
      _ { V2::RateQuery.new(date: "not-a-date") }.must_raise(V2::RateQuery::ValidationError)
    end

    it "raises on conflicting date params" do
      date = Fixtures.latest_date.to_s

      _ { V2::RateQuery.new(date:, from: (Fixtures.latest_date - 30).to_s) }.must_raise(V2::RateQuery::ValidationError)
    end

    it "raises on invalid group" do
      _ { V2::RateQuery.new(group: "day") }.must_raise(V2::RateQuery::ValidationError)
    end

    it "accepts valid group" do
      range_start = (Fixtures.latest_date - 90).to_s
      range_end = Fixtures.latest_date.to_s
      query = V2::RateQuery.new(from: range_start, to: range_end, group: "month")

      _(query.to_a).wont_be_empty
    end

    it "filters by quotes" do
      query = V2::RateQuery.new(quotes: "USD,GBP")
      quotes = query.to_a.map { |r| r[:quote] }.uniq.sort

      _(quotes).must_equal(["GBP", "USD"])
    end

    it "returns empty array for dates before dataset" do
      query = V2::RateQuery.new(date: "1901-01-01")

      _(query.to_a).must_be_empty
    end

    it "raises on invalid base currency" do
      _ { V2::RateQuery.new(base: "FOO") }.must_raise(V2::RateQuery::ValidationError)
    end

    it "raises on invalid quote currency" do
      _ { V2::RateQuery.new(quotes: "USD,FOO") }.must_raise(V2::RateQuery::ValidationError)
    end

    it "raises on invalid base and quotes together" do
      error = _ { V2::RateQuery.new(base: "FOO", quotes: "BAR") }.must_raise(V2::RateQuery::ValidationError)
      _(error.message).must_include("FOO")
      _(error.message).must_include("BAR")
    end

    it "snaps to nearest business day" do
      sunday = Fixtures.recent_sunday
      friday = Fixtures.preceding_friday(sunday)
      query = V2::RateQuery.new(date: sunday.to_s)

      _(query.to_a.first[:date]).must_equal(friday.to_s)
    end

    describe "with pegged currencies" do
      # AED is pegged to USD at exactly 3.6725. When a provider reports EUR/AED
      # and we query base=USD, the blender computes USD/AED via EUR cross rates,
      # introducing rounding noise. The rate should snap to the exact peg value.
      before do
        date = Fixtures.latest_date
        # ECB has EUR/USD in fixtures; add EUR/AED and EUR/SAR so the blender
        # computes cross rates (e.g. EUR/AED ÷ EUR/USD), introducing rounding noise.
        Rate.dataset.multi_insert([
          { provider: "ECB", date:, base: "EUR", quote: "AED", rate: 3.97 },
          { provider: "ECB", date:, base: "EUR", quote: "SAR", rate: 4.05 },
        ])
      end

      it "snaps to exact peg rate when base matches peg anchor" do
        query = V2::RateQuery.new(date: Fixtures.latest_date.to_s, base: "USD", quotes: "AED")
        results = query.to_a

        _(results).wont_be_empty
        _(results.first[:rate]).must_equal(3.6725)
      end

      it "snaps cross-peg rates between two pegged currencies" do
        query = V2::RateQuery.new(date: Fixtures.latest_date.to_s, base: "AED", quotes: "SAR")
        results = query.to_a

        _(results).wont_be_empty

        expected = 3.75 / 3.6725

        _(results.first[:rate]).must_be_close_to(expected, 0.0001)
      end
    end

    describe "range carry-forward" do
      # Insert Friday data from 5 providers + Saturday data from 1 weekend provider.
      # Verify carry-forward gives Saturday enough providers for consensus.
      before do
        friday = Fixtures.preceding_friday(Fixtures.recent_sunday)
        saturday = friday + 1
        @friday = friday
        @saturday = saturday

        providers = ["ECB", "BOC", "BOJ", "FRED", "TCMB"]
        providers.each do |p|
          Rate.dataset.insert(date: friday, base: "EUR", quote: "XTS", rate: 1.10, provider: p)
        end
        Rate.dataset.insert(date: saturday, base: "EUR", quote: "XTS", rate: 1.11, provider: "BNM")
      end

      it "carries forward weekday providers into weekend blends" do
        query = V2::RateQuery.new(from: @friday.to_s, to: @saturday.to_s)
        results = query.to_a

        saturday_results = results.select { |r| r[:date] == @saturday.to_s && r[:quote] == "XTS" }

        _(saturday_results).wont_be_empty
      end

      it "does not carry forward beyond the lookback window" do
        old_date = @saturday - 10
        Rate.dataset.insert(date: old_date, base: "EUR", quote: "XTS", rate: 9.99, provider: "SARB")

        query = V2::RateQuery.new(from: @friday.to_s, to: @saturday.to_s)
        results = query.to_a

        saturday_results = results.select { |r| r[:date] == @saturday.to_s && r[:quote] == "XTS" }
        # Rate should be close to 1.10-1.11, not skewed by 9.99
        _(saturday_results.first[:rate]).must_be_close_to(1.10, 0.05)
      end
    end

    describe "peg gap filling" do
      # BTN is pegged 1:1 to INR (since 1974). Fixtures have ECB providing INR.
      # Add a provider that starts covering BTN only recently, leaving older dates
      # to be filled by the peg.
      before do
        cutoff = Fixtures.business_day(30)
        days = []
        date = Fixtures.latest_date
        while date >= cutoff
          days << date unless date.saturday? || date.sunday?
          date -= 1
        end

        records = days.map do |date|
          { provider: "TEST", date:, base: "EUR", quote: "BTN", rate: 90.0 }
        end
        Rate.dataset.multi_insert(records)
      end

      it "fills dates before provider coverage with peg-derived rates" do
        early_date = Fixtures.business_day(60)
        query = V2::RateQuery.new(date: early_date.to_s, quotes: "BTN")
        results = query.to_a

        _(results).wont_be_empty
        _(results.first[:quote]).must_equal("BTN")
      end

      it "uses provider rates when available" do
        recent_date = Fixtures.latest_date.to_s
        query = V2::RateQuery.new(date: recent_date, quotes: "BTN")
        results = query.to_a

        _(results).wont_be_empty
        _(results.first[:quote]).must_equal("BTN")
      end
    end
  end
end
