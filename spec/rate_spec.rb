# frozen_string_literal: true

require_relative "helper"
require "rate"

describe Rate do
  describe ".latest" do
    it "returns latest available rates on given date" do
      date = Fixtures.latest_date
      data = Rate.latest(date)

      _(data.to_a.sample.date).must_equal(date)
    end

    it "snaps to nearest prior date when requested date has no rates" do
      sunday = Fixtures.recent_sunday
      friday = Fixtures.preceding_friday(sunday)
      data = Rate.where(provider: "ECB").latest(sunday)

      _(data.map(&:date).uniq).must_equal([friday])
    end

    it "includes each provider's most recent date" do
      data = Rate.latest(Fixtures.latest_date)
      providers = data.map(&:provider).uniq.sort

      _(providers).must_include("ECB")
      _(providers).must_include("BOC")
    end

    it "excludes providers more than 14 days behind the global max" do
      date = Fixtures.latest_date
      Rate.dataset.insert(date: date - 20, base: "EUR", quote: "XTS", rate: 1.08, provider: "STALE")
      Rate.dataset.insert(date: date - 21, base: "EUR", quote: "XTS", rate: 1.07, provider: "STALE")

      data = Rate.latest(date)
      providers = data.map(&:provider).uniq

      _(providers).must_include("ECB")
      _(providers).wont_include("STALE")
    end

    it "includes providers within 14 days of the global max" do
      date = Fixtures.latest_date
      Rate.dataset.insert(date: date - 10, base: "USD", quote: "XTS", rate: 0.92, provider: "FRED")
      Rate.dataset.insert(date: date - 17, base: "USD", quote: "XTS", rate: 0.91, provider: "FRED")

      data = Rate.latest(date)
      providers = data.map(&:provider).uniq

      _(providers).must_include("ECB")
      _(providers).must_include("FRED")
    end

    it "includes rates from different dates within the same provider" do
      date = Fixtures.latest_date
      older_date = date - 3
      Rate.dataset.insert(date: older_date, base: "XTS", quote: "PLN", rate: 0.05, provider: "ECB")

      data = Rate.latest(date).all
      quotes = data.select { |r| r.provider == "ECB" }.map(&:quote)

      _(quotes).must_include("PLN")
    end

    it "returns nothing if date predates dataset" do
      _(Rate.latest(Date.parse("1901-01-01"))).must_be_empty
    end

    it "returns latest rates when client date is ahead of server" do
      future_date = Date.today + 1
      data = Rate.latest(future_date)

      _(data).wont_be_empty
      _(data.map(:date).uniq.sort).must_equal(Rate.latest.map(:date).uniq.sort)
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
    it "filters symbols" do
      iso_codes = ["CAD", "USD"]
      data = Rate.where(provider: "ECB").latest(Fixtures.latest_date).only(*iso_codes).all

      _(data.map(&:quote).sort).must_equal(iso_codes)
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

  describe "outlier filtering" do
    it "excludes outliers from queries by default" do
      date = Fixtures.latest_date
      Rate.dataset.insert(date:, base: "EUR", quote: "XTS", rate: 999.0, provider: "ECB", outlier: true)

      quotes = Rate.where(provider: "ECB", date:).map(:quote)

      _(quotes).wont_include("XTS")
    end

    it "includes outliers when unfiltered" do
      date = Fixtures.latest_date
      Rate.dataset.insert(date:, base: "EUR", quote: "XTS", rate: 999.0, provider: "ECB", outlier: true)

      quotes = Rate.unfiltered.where(provider: "ECB", date:).map(:quote)

      _(quotes).must_include("XTS")
    end
  end
end
