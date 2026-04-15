# frozen_string_literal: true

require_relative "helper"
require "rate"
require "carry_forward"

describe Rate do
  describe CarryForward, ".latest" do
    it "returns latest available rates on given date" do
      date = Fixtures.latest_date
      rows = Rate.where(date: (date - 14)..date).naked.all
      data = CarryForward.latest(rows, date:)

      _(data.sample[:date]).must_equal(date)
    end

    it "snaps to nearest prior date when requested date has no rates" do
      sunday = Fixtures.recent_sunday
      friday = Fixtures.preceding_friday(sunday)
      rows = Rate.where(provider: "ECB", date: (sunday - 14)..sunday).naked.all
      data = CarryForward.latest(rows, date: sunday)

      _(data.map { |r| r[:date] }.uniq).must_equal([friday])
    end

    it "includes each provider's most recent date" do
      date = Fixtures.latest_date
      rows = Rate.where(date: (date - 14)..date).naked.all
      data = CarryForward.latest(rows, date:)
      providers = data.map { |r| r[:provider] }.uniq.sort

      _(providers).must_include("ECB")
      _(providers).must_include("BOC")
    end

    it "excludes providers more than 14 days behind the target" do
      date = Fixtures.latest_date
      Rate.dataset.insert(date: date - 20, base: "EUR", quote: "XTS", rate: 1.08, provider: "STALE")

      rows = Rate.where(date: (date - 14)..date).naked.all
      data = CarryForward.latest(rows, date:)
      providers = data.map { |r| r[:provider] }.uniq

      _(providers).must_include("ECB")
      _(providers).wont_include("STALE")
    end

    it "includes providers within 14 days of the target" do
      date = Fixtures.latest_date
      Rate.dataset.insert(date: date - 10, base: "USD", quote: "XTS", rate: 0.92, provider: "FRED")

      rows = Rate.where(date: (date - 14)..date).naked.all
      data = CarryForward.latest(rows, date:)
      providers = data.map { |r| r[:provider] }.uniq

      _(providers).must_include("ECB")
      _(providers).must_include("FRED")
    end

    it "includes rates from different dates within the same provider" do
      date = Fixtures.latest_date
      older_date = date - 3
      Rate.dataset.insert(date: older_date, base: "XTS", quote: "PLN", rate: 0.05, provider: "ECB")

      rows = Rate.where(date: (date - 14)..date).naked.all
      data = CarryForward.latest(rows, date:)
      quotes = data.select { |r| r[:provider] == "ECB" }.map { |r| r[:quote] }

      _(quotes).must_include("PLN")
    end

    it "returns nothing if date predates dataset" do
      date = Date.parse("1901-01-01")
      rows = Rate.where(date: (date - 14)..date).naked.all

      _(CarryForward.latest(rows, date:)).must_be_empty
    end

    it "returns latest rates when client date is ahead of server" do
      future_date = Date.today + 1
      rows = Rate.where(date: (future_date - 14)..future_date).naked.all
      data = CarryForward.latest(rows, date: future_date)

      _(data).wont_be_empty

      today_rows = Rate.where(date: (Date.today - 14)..Date.today).naked.all
      today_data = CarryForward.latest(today_rows, date: Date.today)

      _(data.map { |r| r[:date] }.uniq.sort).must_equal(today_data.map { |r| r[:date] }.uniq.sort)
    end
  end

  describe ".between" do
    it "returns rates between given working dates" do
      start_date = Fixtures.latest_date - 30
      end_date = Fixtures.latest_date
      dates = Rate.between(start_date..end_date).map(:date).sort.uniq

      _(dates.first).must_be(:<=, start_date)
      _(dates.last).must_equal(end_date)
    end

    it "starts on preceding business day if start date is a weekend" do
      sunday = Fixtures.recent_sunday
      monday = sunday + 1
      friday = Fixtures.preceding_friday(sunday)
      dates = Rate.between(sunday..monday).map(:date).uniq

      _(dates).must_include(friday)
    end

    it "returns nothing if end date predates dataset" do
      interval = (Date.parse("1901-01-01")..Date.parse("1901-01-31"))

      _(Rate.between(interval)).must_be_empty
    end

    it "allows start date to predate dataset" do
      start_date = Date.parse("1901-01-01")
      end_date = Fixtures.latest_date
      dates = Rate.between(start_date..end_date).map(:date)

      _(dates).wont_be_empty
    end

    it "returns nothing when start date is in the future" do
      start_date = Date.today + 1
      end_date = start_date + 1
      dates = Rate.between(start_date..end_date).map(:date)

      _(dates).must_be_empty
    end
  end

  describe ".only" do
    it "returns only rate pairs involving the given currencies" do
      currencies = ["CAD", "USD"]
      date = Fixtures.latest_date
      data = Rate.where(date: (date - 14)..date).only(*currencies).all

      data.each do |r|
        _([r.base, r.quote].any? { |c| currencies.include?(c) }).must_equal(true)
      end
    end

    it "includes rates from providers where currency appears as base" do
      date = Fixtures.latest_date
      data = Rate.where(date: (date - 14)..date).only("USD", "EUR").all
      providers = data.map(&:provider).uniq

      _(providers).must_include("BOC")
    end

    it "includes providers whose rates span both sides of the pair" do
      date = Fixtures.latest_date
      data = Rate.where(date: (date - 14)..date).only("JPY", "EUR").all
      providers = data.map(&:provider).uniq

      _(providers).must_include("BOJ")
    end

    it "returns nothing if no matches" do
      _(Rate.only("FOO").all).must_be_empty
    end
  end

  describe ".downsample" do
    let(:interval) { (Fixtures.latest_date - 366)..Fixtures.latest_date }

    it "groups by week" do
      dates = Rate.between(interval).downsample("week")

      _(dates.map(:date).uniq.count).must_be(:<=, 55)
    end

    it "groups by month" do
      dates = Rate.between(interval).downsample("month")

      _(dates.map(:date).uniq.count).must_be(:<=, 14)
    end

    it "sorts by date" do
      dates = Rate.between(interval).downsample("week").map(:date)

      _(dates).must_equal(dates.sort)
    end
  end
end
