# frozen_string_literal: true

require_relative "helper"
require "currency"

describe Currency do
  describe ".latest" do
    it "returns latest available rates on given date" do
      date = Date.parse("2010-01-04")
      data = Currency.latest(date)
      _(data.to_a.sample.date).must_equal(date)

      date = Date.parse("2010-01-01")
      data = Currency.latest(date)
      _(data.to_a.sample.date).must_equal(Date.parse("2009-12-31"))
    end

    it "returns nothing if date predates dataset" do
      _(Currency.latest(Date.parse("1901-01-01"))).must_be_empty
    end

    it "returns latest available rates for future dates" do
      future_date = Date.today + 1
      data = Currency.latest(future_date)
      _(data).wont_be_empty
      _(data.to_a.sample.date).must_equal(Currency.latest.to_a.sample.date)
    end
  end

  describe ".between" do
    it "returns rates between given working dates" do
      start_date = Date.parse("2010-01-04")
      end_date = Date.parse("2010-01-29")
      dates = Currency.between(start_date..end_date).map(:date).sort.uniq
      _(dates.first).must_equal(start_date)
      _(dates.last).must_equal(end_date)
    end

    it "starts on preceding business day if start date is a holiday" do
      start_date = Date.parse("2024-11-03")
      end_date = Date.parse("2024-11-04")
      dates = Currency.between(start_date..end_date).map(:date).uniq
      _(dates).must_include(Date.parse("2024-11-01"))
    end

    it "returns nothing if end date predates dataset" do
      interval = (Date.parse("1901-01-01")..Date.parse("1901-01-31"))
      _(Currency.between(interval)).must_be_empty
    end

    it "allows start date to predate dataset" do
      start_date = Date.parse("1901-01-01")
      end_date = Date.parse("2024-01-01")
      dates = Currency.between(start_date..end_date).map(:date)
      _(dates).wont_be_empty
    end

    it "returns nothing when start date is in the future" do
      start_date = Date.today + 1
      end_date = start_date + 1
      dates = Currency.between(start_date..end_date).map(:date)
      _(dates).must_be_empty
    end
  end

  describe ".only" do
    it "filters symbols" do
      iso_codes = ["CAD", "USD"]
      data = Currency.latest.only(*iso_codes).all
      _(data.map(&:iso_code).sort).must_equal(iso_codes)
    end

    it "returns nothing if no matches" do
      _(Currency.only("FOO").all).must_be_empty
    end
  end

  describe ".between with sampling" do
    let(:day) { Date.parse("2010-01-01") }

    it "returns everything up to a year" do
      interval = day..day + 365
      dates = Currency.between(interval)
      _(dates.map(:date).uniq.count).must_be(:>, 52)
    end

    it "can sample weekly" do
      interval = day..day + 366
      dates = Currency.between(interval).sample("week")
      _(dates.map(:date).uniq.count).must_be(:<, 54)
    end

    it "sorts by date when sampling" do
      interval = day..day + 366
      dates = Currency.between(interval).sample("week").map(:date)
      _(dates).must_equal(dates.sort)
    end
  end
end
