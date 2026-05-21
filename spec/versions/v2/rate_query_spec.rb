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

    describe "single-day queries" do
      it "snap each pair to its most recent publication when the requested date is silent" do
        sunday = Fixtures.recent_sunday
        friday = Fixtures.preceding_friday(sunday)
        query = V2::RateQuery.new(date: sunday.to_s)

        _(query.to_a.first[:date]).must_equal(friday.to_s)
      end

      it "stamp each row with its pair's actual observation date" do
        stale_date = Fixtures.latest_date - 5
        Rate.dataset.insert(date: stale_date, base: "EUR", quote: "RON", rate: 4.97, provider: "ECB")

        query = V2::RateQuery.new(date: Fixtures.latest_date.to_s)
        results = query.to_a

        ron = results.find { |r| r[:base] == "EUR" && r[:quote] == "RON" }
        usd = results.find { |r| r[:base] == "EUR" && r[:quote] == "USD" }

        _(ron[:date]).must_equal(stale_date.to_s)
        _(usd[:date]).must_equal(Fixtures.latest_date.to_s)
      end
    end

    describe "range queries" do
      let(:monday) { Fixtures.gap_boundary_monday }

      it "emit rows for a pair only on days that pair was actually published" do
        friday_before = monday - 3
        saturday = monday - 2
        sunday = monday - 1
        friday_after = monday + 4

        Rate.dataset.insert(date: monday, base: "EUR", quote: "RON", rate: 4.97, provider: "ECB")

        query = V2::RateQuery.new(from: friday_before.to_s, to: friday_after.to_s)
        results = query.to_a

        dates = results.map { |r| r[:date] }

        _(dates).wont_include(saturday.to_s)
        _(dates).wont_include(sunday.to_s)

        ron_rows = results.select { |r| r[:base] == "EUR" && r[:quote] == "RON" }

        _(ron_rows.size).must_equal(1)
        _(ron_rows.first[:date]).must_equal(monday.to_s)
      end

      it "surface a silent pair's most recent publication via carry-forward" do
        range_end = monday + 5
        pre_range_date = monday - 5
        in_range_date = monday + 2
        Rate.dataset.insert(date: pre_range_date, base: "EUR", quote: "RON", rate: 4.97, provider: "ECB")
        Rate.dataset.insert(date: in_range_date, base: "EUR", quote: "RON", rate: 4.95, provider: "ECB")

        query = V2::RateQuery.new(from: monday.to_s, to: range_end.to_s)
        ron_dates = query.to_a.select { |r| r[:base] == "EUR" && r[:quote] == "RON" }.map { |r| r[:date] }.sort

        _(ron_dates).must_equal([pre_range_date.to_s, in_range_date.to_s])
      end

      it "snap back to the most recent prior active day when the range starts globally silent" do
        prior_friday = monday - 3
        saturday = monday - 2
        sunday = monday - 1

        partial_dates = V2::RateQuery.new(from: sunday.to_s, to: monday.to_s, quotes: "USD").to_a
          .map { |r| r[:date] }.uniq.sort

        _(partial_dates).must_include(prior_friday.to_s)
        _(partial_dates).must_include(monday.to_s)

        fully_silent_dates = V2::RateQuery.new(from: saturday.to_s, to: sunday.to_s, quotes: "USD").to_a
          .map { |r| r[:date] }.uniq

        _(fully_silent_dates).must_equal([prior_friday.to_s])
      end

      it "carry forward silent providers into the blend" do
        date = Fixtures.latest_date
        Rate.dataset.where(provider: "BOC", date: date).delete

        with_boc = V2::RateQuery.new(from: (date - 3).to_s, to: date.to_s, quotes: "USD").to_a
        Rate.dataset.where(provider: "BOC").delete
        without_boc = V2::RateQuery.new(from: (date - 3).to_s, to: date.to_s, quotes: "USD").to_a

        with_row = with_boc.find { |r| r[:date] == date.to_s }
        without_row = without_boc.find { |r| r[:date] == date.to_s }

        _(with_row).wont_be_nil
        _(without_row).wont_be_nil
        _(with_row[:rate]).wont_equal(without_row[:rate])
      end
    end

    it "returns the same rates for a date whether queried individually or in a range" do
      date = Fixtures.latest_date
      Rate.dataset.where(provider: "BOC", date: date).delete

      single = V2::RateQuery.new(date: date.to_s, quotes: "USD").to_a
      range = V2::RateQuery.new(from: (date - 3).to_s, to: date.to_s, quotes: "USD").to_a
        .select { |r| r[:date] == date.to_s }

      _(single).wont_be_empty
      _(range).wont_be_empty

      _(range.first[:rate]).must_equal(single.first[:rate])
    end

    it "returns the same rates for a date with providers scope" do
      date = Fixtures.latest_date
      single = V2::RateQuery.new(date: date.to_s, providers: "ECB", quotes: "USD").to_a
      range = V2::RateQuery.new(from: (date - 3).to_s, to: date.to_s, providers: "ECB", quotes: "USD").to_a
        .select { |r| r[:date] == date.to_s }

      _(single).wont_be_empty
      _(range).wont_be_empty

      _(range.first[:rate]).must_equal(single.first[:rate])
    end

    describe "with expand=providers" do
      it "is omitted by default" do
        query = V2::RateQuery.new(date: Fixtures.latest_date.to_s, quotes: "USD")
        results = query.to_a

        _(results).wont_be_empty
        _(results.first.key?(:providers)).must_equal(false)
      end

      it "adds providers list to blended rows as {key, rate} objects" do
        query = V2::RateQuery.new(date: Fixtures.latest_date.to_s, quotes: "USD", expand: "providers")
        results = query.to_a

        _(results).wont_be_empty
        providers = results.first[:providers]

        _(providers).must_be_kind_of(Array)
        _(providers).wont_be_empty
        _(providers.first).must_be_kind_of(Hash)
        _(providers.first.keys.sort).must_equal([:key, :rate])
        _(providers.first[:key]).must_be_kind_of(String)
        _(providers.first[:rate]).must_be_kind_of(Numeric)
      end

      it "marks all providers excluded on peg-snapped rows" do
        date = Fixtures.latest_date
        Rate.dataset.insert(provider: "ECB", date:, base: "EUR", quote: "AED", rate: 3.97)

        query = V2::RateQuery.new(date: date.to_s, base: "USD", quotes: "AED", expand: "providers")
        results = query.to_a

        _(results).wont_be_empty
        _(results.first[:rate]).must_equal(3.6725)
        providers = results.first[:providers]

        _(providers).wont_be_empty
        _(providers.all? { |p| p[:excluded] == true }).must_equal(true)
      end

      it "works with rollup queries" do
        range_start = (Fixtures.latest_date - 90).to_s
        range_end = Fixtures.latest_date.to_s
        query = V2::RateQuery.new(from: range_start, to: range_end, group: "week", quotes: "USD", expand: "providers")
        results = query.to_a

        _(results).wont_be_empty
        providers = results.first[:providers]

        _(providers).must_be_kind_of(Array)
        _(providers).wont_be_empty
        _(providers.first.keys.sort).must_equal([:key, :rate])
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

    describe "#scale_for_pegged_base" do
      it "drops the base->base row that PegAnchor synthesizes for the user's pegged base" do
        date = Date.parse("2024-01-15")
        query = V2::RateQuery.new(base: "AED")
        rows = [
          { date:, base: "USD", quote: "EUR", rate: 0.93 },
          { date:, base: "USD", quote: "AED", rate: 3.6725 },
        ]

        result = query.send(:scale_for_pegged_base, rows)

        _(result.find { |r| r[:quote] == "AED" }).must_be_nil
        _(result.find { |r| r[:quote] == "USD" }[:rate]).must_be_close_to(1.0 / 3.6725)
      end
    end

    describe "?providers= with pegged base" do
      it "returns empty (peg layer is bypassed when source set is restricted)" do
        recent_date = Fixtures.latest_date.to_s
        query = V2::RateQuery.new(date: recent_date, providers: "ECB", base: "AED", quotes: "USD")

        _(query.to_a).must_be_empty
      end
    end

    describe "#derive" do
      let(:date) { Date.parse("2024-01-15") }

      def call_derive(rows, target:)
        V2::RateQuery.allocate.send(:derive, rows, target: target)
      end

      it "returns [] when target is not in input" do
        rows = [{ date:, base: "USD", quote: "EUR", rate: 0.93 }]

        _(call_derive(rows, target: "GBP")).must_equal([])
      end

      it "returns [] when input is empty" do
        _(call_derive([], target: "GBP")).must_equal([])
      end

      it "rebases each row to target by division and appends target->pivot row" do
        rows = [
          { date:, base: "USD", quote: "EUR", rate: 0.93, providers: [{ key: "ECB", rate: 0.93 }] },
          { date:, base: "USD", quote: "GBP", rate: 0.79, providers: [{ key: "ECB", rate: 0.79 }] },
        ]

        result = call_derive(rows, target: "EUR")
        gbp = result.find { |r| r[:quote] == "GBP" }
        usd = result.find { |r| r[:quote] == "USD" }

        _(gbp[:base]).must_equal("EUR")
        _(gbp[:rate]).must_be_close_to(0.79 / 0.93)
        _(gbp[:providers].first[:rate]).must_be_close_to(0.79 / 0.93)
        _(usd[:base]).must_equal("EUR")
        _(usd[:rate]).must_be_close_to(1.0 / 0.93)
        _(usd[:providers].first[:rate]).must_be_close_to(1.0 / 0.93)
      end

      it "drops the target row from output (no base->base row)" do
        rows = [
          { date:, base: "USD", quote: "EUR", rate: 0.93 },
          { date:, base: "USD", quote: "GBP", rate: 0.79 },
        ]

        result = call_derive(rows, target: "EUR")

        _(result.find { |r| r[:quote] == "EUR" }).must_be_nil
      end

      it "produces exact reciprocals between any two non-pivot quotes" do
        rows = [
          { date:, base: "USD", quote: "EUR", rate: 0.93 },
          { date:, base: "USD", quote: "GBP", rate: 0.79 },
        ]

        eur_view = call_derive(rows, target: "EUR")
        gbp_view = call_derive(rows, target: "GBP")
        eur_to_gbp = eur_view.find { |r| r[:quote] == "GBP" }[:rate]
        gbp_to_eur = gbp_view.find { |r| r[:quote] == "EUR" }[:rate]

        _(eur_to_gbp * gbp_to_eur).must_be_close_to(1.0, 1e-12)
      end
    end

    describe "#fast_path?" do
      let(:date) { Date.parse("2024-01-15") }

      def call_fast_path(query, rows)
        query.send(:fast_path?, rows)
      end

      it "is true when every row's :base equals effective_base" do
        query = V2::RateQuery.new(base: "EUR")
        rows = [
          { date:, base: "EUR", quote: "USD", rate: 1.08, provider: "ECB" },
          { date:, base: "EUR", quote: "GBP", rate: 0.86, provider: "ECB" },
        ]

        _(call_fast_path(query, rows)).must_equal(true)
      end

      it "is false when any row has a different :base" do
        query = V2::RateQuery.new(base: "EUR")
        rows = [
          { date:, base: "EUR", quote: "USD", rate: 1.08, provider: "ECB" },
          { date:, base: "CAD", quote: "USD", rate: 0.74, provider: "BOC" },
        ]

        _(call_fast_path(query, rows)).must_equal(false)
      end

      it "uses the peg's base as effective_base for a pegged request base" do
        query = V2::RateQuery.new(base: "AED")
        rows = [
          { date:, base: "USD", quote: "EUR", rate: 0.93, provider: "ECB" },
        ]

        _(call_fast_path(query, rows)).must_equal(true)
      end

      it "is true on empty input (vacuous)" do
        query = V2::RateQuery.new(base: "EUR")

        _(call_fast_path(query, [])).must_equal(true)
      end
    end
  end
end
