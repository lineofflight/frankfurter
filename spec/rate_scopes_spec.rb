# frozen_string_literal: true

require_relative "helper"
require "rate"
require "provider"
require "blended_weekly_rate"
require "blended_monthly_rate"

describe RateScopes do
  it "excludes unknown currencies on either side from daily blends" do
    rows = [
      { base: "USD", quote: "EUR", mid: 0.9 },
      { base: "USD", quote: "SDR", mid: 2.0 },
      { base: "SDR", quote: "EUR", mid: 3.0 },
    ]
    rows.each { |row| Rate.dataset.insert(**row, provider: "ECB", date: "2000-01-03") }

    _(Rate.where(date: "2000-01-03").blendable.select_map([:base, :quote])).must_equal([["USD", "EUR"]])
  end

  it "recognizes registered aliases on either side of daily and grouped observations" do
    _(Money::Currency.find("GHC")).wont_be_nil
    _(Money::Currency.table[:ghc][:iso_code]).must_equal("GHS")
    pairs = [{ base: "USD", quote: "GHC" }, { base: "GHC", quote: "USD" }]
    [Rate, WeeklyRate, MonthlyRate].each do |model|
      value = model == Rate ? :mid : :rate
      pairs.each do |pair|
        model.dataset.insert(**pair, provider: "ECB", model.date_column => "2000-01-03", value => 3.0)
      end

      rows = model.where(model.date_column => "2000-01-03").blendable.order(:base).select_map([:base, :quote])

      _(rows).must_equal([["GHC", "USD"], ["USD", "GHC"]])
    end
  end

  it "excludes observations on or after either currency's terminal date" do
    ["2016-06-30", "2016-07-01", "2016-07-02"].each do |date|
      Rate.dataset.insert(provider: "ECB", date:, base: "USD", quote: "BYR", mid: 20000.0)
      Rate.dataset.insert(provider: "ECB", date:, base: "BYR", quote: "USD", mid: 0.00005)
    end

    rows = Rate.where(Sequel.|({ base: "BYR" }, { quote: "BYR" })).blendable

    _(rows.count).must_equal(2)
    _(rows.select_map(:date).uniq).must_equal([Date.new(2016, 6, 30)])
  end

  it "keeps the final sucre day and excludes its retirement date" do
    ["2000-09-08", "2000-09-09"].each do |date|
      Rate.dataset.insert(provider: "ECB", date:, base: "USD", quote: "ECS", mid: 25000.0)
    end

    rows = Rate.where(quote: "ECS").blendable

    _(rows.select_map(:date)).must_equal([Date.new(2000, 9, 8)])
  end

  sides = [:base, :quote]
  [[:week, BlendedWeeklyRate, "BYR", "2016-06-30", "2016-07-01"],
   [:month, BlendedMonthlyRate, "VEF", "2018-08-19", "2018-08-20"],].each do |precision, model, code, before, terminal|
    sides.each do |side|
      it "preserves the stored #{precision} boundary #{side} average when daily observations are all eligible" do
        pair = side == :base ? { base: code, quote: "USD" } : { base: "USD", quote: code }
        bucket = DB.get(Bucket.expression(precision, before))
        Rate.dataset.insert(**pair, provider: "ECB", date: before, mid: 15.123456789012)
        model.source.dataset.insert(**pair, provider: "ECB", bucket_date: bucket, rate: 15.123456789011)

        scope = model.source.blendable.where(**pair, provider: "ECB", bucket_date: bucket)

        _(scope.get(:rate)).must_equal(15.123456789011)
      end

      it "preserves the stored #{precision} boundary #{side} average when no daily observations remain" do
        pair = side == :base ? { base: code, quote: "USD" } : { base: "USD", quote: code }
        bucket = DB.get(Bucket.expression(precision, before))
        model.source.dataset.insert(**pair, provider: "ECB", bucket_date: bucket, rate: 15.123456789011)

        scope = model.source.blendable.where(**pair, provider: "ECB", bucket_date: bucket)

        _(scope.get(:rate)).must_equal(15.123456789011)
      end

      it "keeps #{precision} blends identical when retained expired #{side} rows share a bucket" do
        pair = side == :base ? { base: code, quote: "USD" } : { base: "USD", quote: code }
        dates = [Date.parse(before), Date.parse(terminal)]
        bucket = DB.get(Bucket.expression(precision, before))
        Rate.dataset.insert(**pair, provider: "ECB", date: dates.first, mid: 15.123456789012)
        Provider["ECB"].send(:refresh_rollups, [dates.first])
        original = model.where(bucket_date: bucket).order(:quote).naked.all

        Rate.dataset.insert(**pair, provider: "ECB", date: dates.last, mid: 3000.0)
        Rate.dataset.insert(provider: "ECB", date: dates.last, base: "USD", quote: "SDR", mid: 5000.0)
        Provider["ECB"].send(:refresh_rollups, dates)

        published = model.source.where(**pair, provider: "ECB", bucket_date: bucket).get(:rate)
        blended = model.source.blendable.where(**pair, provider: "ECB", bucket_date: bucket).get(:rate)

        _(published).must_equal((15.123456789012 + 3000.0) / 2)
        _(model.source.where(provider: "ECB", bucket_date: bucket, quote: "SDR").count).must_equal(1)
        _(model.where(bucket_date: bucket).order(:quote).naked.all).must_equal(original)
        _(blended).must_equal(15.123456789012)
        _(model.source.blendable.where(quote: "SDR").count).must_equal(0)
      end
    end

    it "omits #{precision} pairs whose bucket contains only expired observations" do
      date = Date.parse(terminal)
      Rate.dataset.insert(provider: "ECB", date:, base: "USD", quote: code, mid: 30.0)
      Provider["ECB"].send(:refresh_rollups, [date])
      bucket = DB.get(Bucket.expression(precision, terminal))

      _(model.source.where(bucket_date: bucket, quote: code).count).must_equal(1)
      _(model.source.blendable.where(bucket_date: bucket, quote: code).count).must_equal(0)
      _(model.where(bucket_date: bucket, quote: code).count).must_equal(0)
    end

    it "uses stored #{precision} rollups for pairs unaffected by a terminal date" do
      date = Date.new(2000, 1, 1)
      model.source.dataset.insert(provider: "ECB", bucket_date: date, base: "USD", quote: "EUR", rate: 0.876543210123)

      _(model.source.blendable.where(bucket_date: date).get(:rate)).must_equal(0.876543210123)
    end
  end
end
