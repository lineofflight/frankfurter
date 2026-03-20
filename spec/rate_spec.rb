# frozen_string_literal: true

require_relative "helper"
require "rate"

describe Rate do
  describe ".latest" do
    it "returns latest available rates on given date" do
      date = Date.parse("2010-01-04")
      data = Rate.latest(date)

      _(data.to_a.sample.date).must_equal(date)

      date = Date.parse("2009-12-30")
      data = Rate.latest(date)

      _(data.to_a.sample.date).must_equal(Date.parse("2009-12-30"))
    end

    it "includes each provider's most recent date" do
      Rate.dataset.insert(date: Date.parse("2024-01-15"), base: "EUR", quote: "XTS", rate: 1.08, provider: "ECB")
      Rate.dataset.insert(date: Date.parse("2024-01-14"), base: "EUR", quote: "XTS", rate: 1.08, provider: "ECB")
      Rate.dataset.insert(date: Date.parse("2024-01-14"), base: "CAD", quote: "XTS", rate: 1.35, provider: "BOC")
      Rate.dataset.insert(date: Date.parse("2024-01-13"), base: "CAD", quote: "XTS", rate: 1.34, provider: "BOC")

      data = Rate.latest(Date.parse("2024-01-15"))
      providers = data.map(&:provider).uniq.sort

      _(providers).must_include("ECB")
      _(providers).must_include("BOC")
    end

    it "excludes providers outside their publish frequency" do
      Rate.dataset.insert(date: Date.parse("2024-01-10"), base: "EUR", quote: "XTS", rate: 1.08, provider: "STALE")
      Rate.dataset.insert(date: Date.parse("2024-01-09"), base: "EUR", quote: "XTS", rate: 1.07, provider: "STALE")
      Rate.dataset.insert(date: Date.parse("2024-01-15"), base: "EUR", quote: "XTS", rate: 1.09, provider: "ECB")
      Rate.dataset.insert(date: Date.parse("2024-01-14"), base: "EUR", quote: "XTS", rate: 1.08, provider: "ECB")

      data = Rate.latest(Date.parse("2024-01-15"))
      providers = data.map(&:provider).uniq

      _(providers).must_include("ECB")
      _(providers).wont_include("STALE")
    end

    it "includes weekly providers within their cadence" do
      Rate.dataset.insert(date: Date.parse("2024-01-15"), base: "USD", quote: "XTS", rate: 0.92, provider: "FRED")
      Rate.dataset.insert(date: Date.parse("2024-01-08"), base: "USD", quote: "XTS", rate: 0.91, provider: "FRED")
      Rate.dataset.insert(date: Date.parse("2024-01-19"), base: "EUR", quote: "XTS", rate: 1.09, provider: "ECB")
      Rate.dataset.insert(date: Date.parse("2024-01-18"), base: "EUR", quote: "XTS", rate: 1.08, provider: "ECB")

      data = Rate.latest(Date.parse("2024-01-19"))
      providers = data.map(&:provider).uniq

      _(providers).must_include("ECB")
      _(providers).must_include("FRED")
    end

    it "returns nothing if date predates dataset" do
      _(Rate.latest(Date.parse("1901-01-01"))).must_be_empty
    end

    it "returns latest available rates for future dates" do
      future_date = Date.today + 1
      data = Rate.latest(future_date)

      _(data).wont_be_empty
      _(data.map(:date).uniq.sort).must_equal(Rate.latest.map(:date).uniq.sort)
    end
  end

  describe ".between" do
    it "returns rates between given working dates" do
      start_date = Date.parse("2010-01-04")
      end_date = Date.parse("2010-01-29")
      dates = Rate.between(start_date..end_date).map(:date).sort.uniq

      _(dates.first).must_equal(start_date)
      _(dates.last).must_equal(end_date)
    end

    it "starts on preceding business day if start date is a holiday" do
      start_date = Date.parse("2024-11-03")
      end_date = Date.parse("2024-11-04")
      dates = Rate.between(start_date..end_date).map(:date).uniq

      _(dates).must_include(Date.parse("2024-11-01"))
    end

    it "returns nothing if end date predates dataset" do
      interval = (Date.parse("1901-01-01")..Date.parse("1901-01-31"))

      _(Rate.between(interval)).must_be_empty
    end

    it "allows start date to predate dataset" do
      start_date = Date.parse("1901-01-01")
      end_date = Date.parse("2024-01-01")
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
      data = Rate.where(provider: "ECB").latest.only(*iso_codes).all

      _(data.map(&:quote).sort).must_equal(iso_codes)
    end

    it "returns nothing if no matches" do
      _(Rate.only("FOO").all).must_be_empty
    end
  end

  describe ".downsample" do
    let(:day) { Date.parse("2010-01-01") }
    let(:interval) { day..day + 366 }

    it "groups by week" do
      dates = Rate.between(interval).downsample("week")

      _(dates.map(:date).uniq.count).must_be(:<, 54)
    end

    it "groups by month" do
      dates = Rate.between(interval).downsample("month")

      _(dates.map(:date).uniq.count).must_be(:<=, 13)
    end

    it "sorts by date" do
      dates = Rate.between(interval).downsample("week").map(:date)

      _(dates).must_equal(dates.sort)
    end
  end
end
