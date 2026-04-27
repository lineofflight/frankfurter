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

    it "uses target date for carried-forward quotes" do
      # Fixtures have ECB/BOC/BOJ on business days only. On Saturday, carry-forward brings in Friday's rates.
      # Without the target_date fix, quotes only present via carry-forward get Friday's date in the Saturday
      # blend, producing duplicate (date, quote) pairs. Add a Saturday row from BOC so Saturday becomes a
      # target date.
      friday = Fixtures.preceding_friday(Fixtures.recent_sunday)
      saturday = friday + 1
      Rate.dataset.insert(date: saturday, base: "CAD", quote: "USD", rate: 0.74, provider: "BOC")

      query = V2::RateQuery.new(from: friday.to_s, to: saturday.to_s)
      results = query.to_a

      pairs = results.map { |r| [r[:date], r[:quote]] }

      _(pairs).must_equal(pairs.uniq)
    end

    describe "with expand=providers" do
      it "is omitted by default" do
        query = V2::RateQuery.new(date: Fixtures.latest_date.to_s, quotes: "USD")
        results = query.to_a

        _(results).wont_be_empty
        _(results.first.key?(:providers)).must_equal(false)
      end

      it "adds providers list to blended rows" do
        query = V2::RateQuery.new(date: Fixtures.latest_date.to_s, quotes: "USD", expand: "providers")
        results = query.to_a

        _(results).wont_be_empty
        _(results.first[:providers]).must_be_kind_of(Array)
        _(results.first[:providers]).wont_be_empty
      end

      it "omits providers field on peg-snapped rows" do
        date = Fixtures.latest_date
        Rate.dataset.insert(provider: "ECB", date:, base: "EUR", quote: "AED", rate: 3.97)

        query = V2::RateQuery.new(date: date.to_s, base: "USD", quotes: "AED", expand: "providers")
        results = query.to_a

        _(results).wont_be_empty
        _(results.first[:rate]).must_equal(3.6725)
        _(results.first.key?(:providers)).must_equal(false)
      end

      it "works with rollup queries" do
        range_start = (Fixtures.latest_date - 90).to_s
        range_end = Fixtures.latest_date.to_s
        query = V2::RateQuery.new(from: range_start, to: range_end, group: "week", quotes: "USD", expand: "providers")
        results = query.to_a

        _(results).wont_be_empty
        _(results.first[:providers]).must_be_kind_of(Array)
        _(results.first[:providers]).wont_be_empty
      end

      it "raises on unknown expand value" do
        _ { V2::RateQuery.new(expand: "weights") }.must_raise(V2::RateQuery::ValidationError)
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
