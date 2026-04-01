# frozen_string_literal: true

require_relative "helper"
require "outlier"

describe Outlier do
  def seed_history(count, rate: 1.1)
    date = Date.today - count - 1
    count.times do |i|
      Rate.unfiltered.insert(
        date: date + i,
        provider: "TEST",
        base: "EUR",
        quote: "USD",
        rate: rate + (i % 5) * 0.01,
      )
    end
  end

  it "flags rates beyond the threshold" do
    seed_history(35)
    Rate.unfiltered.insert(
      date: Date.today, provider: "TEST", base: "EUR", quote: "USD", rate: 999.0,
    )

    Outlier.detect(
      provider: "TEST",
      base: "EUR",
      quote: "USD",
      dates: [Date.today],
      exclude_dates: [Date.today],
      apply: true,
    )

    record = Rate.unfiltered.where(provider: "TEST", date: Date.today).first

    _(record[:outlier]).must_equal(true)
  end

  it "does not flag normal rates" do
    seed_history(35)
    Rate.unfiltered.insert(
      date: Date.today, provider: "TEST", base: "EUR", quote: "USD", rate: 1.12,
    )

    Outlier.detect(
      provider: "TEST",
      base: "EUR",
      quote: "USD",
      dates: [Date.today],
      exclude_dates: [Date.today],
      apply: true,
    )

    record = Rate.unfiltered.where(provider: "TEST", date: Date.today).first

    _(record[:outlier]).must_equal(false)
  end

  it "skips detection with insufficient history" do
    seed_history(5)
    Rate.unfiltered.insert(
      date: Date.today, provider: "TEST", base: "EUR", quote: "USD", rate: 999.0,
    )

    count = Outlier.detect(
      provider: "TEST",
      base: "EUR",
      quote: "USD",
      dates: [Date.today],
      exclude_dates: [Date.today],
    )

    _(count).must_equal(0)

    record = Rate.unfiltered.where(provider: "TEST", date: Date.today).first

    _(record[:outlier]).must_equal(false)
  end

  it "returns count of flagged records" do
    seed_history(35)
    Rate.unfiltered.insert(
      date: Date.today, provider: "TEST", base: "EUR", quote: "USD", rate: 999.0,
    )

    count = Outlier.detect(
      provider: "TEST",
      base: "EUR",
      quote: "USD",
      dates: [Date.today],
      exclude_dates: [Date.today],
    )

    _(count).must_equal(1)
  end

  it "does not update by default" do
    seed_history(35)
    Rate.unfiltered.insert(
      date: Date.today, provider: "TEST", base: "EUR", quote: "USD", rate: 999.0,
    )

    Outlier.detect(
      provider: "TEST",
      base: "EUR",
      quote: "USD",
      dates: [Date.today],
      exclude_dates: [Date.today],
    )

    record = Rate.unfiltered.where(provider: "TEST", date: Date.today).first

    _(record[:outlier]).must_equal(false)
  end

  it "exposes stats for introspection" do
    seed_history(35)

    outlier = Outlier.new(
      provider: "TEST", base: "EUR", quote: "USD", dates: [],
    )

    _(outlier.stats[:mean]).must_be_close_to(1.12, 0.01)
    _(outlier.stats[:sd]).must_be(:>, 0)
  end
end
