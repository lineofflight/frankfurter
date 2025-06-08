# frozen_string_literal: true

require_relative "helper"
require "bank"

describe Bank do
  around do |test|
    Currency.db.transaction do
      Currency.dataset.delete
      test.call
      raise Sequel::Rollback
    end
  end

  before do
    VCR.insert_cassette("feed")
  end

  after do
    VCR.eject_cassette
  end

  def count_unique_dates
    Currency.select(:date).distinct.count
  end

  it "fetches all rates" do
    Bank.fetch_all!

    _(count_unique_dates).must_be(:>, 90)
  end

  it "does not duplicate when fetching all rates" do
    Bank.fetch_all!
    count = count_unique_dates
    Bank.fetch_all!

    _(count_unique_dates).must_equal(count)
  end

  it "fetches rates for last 90 days" do
    Bank.fetch_ninety_days!

    _(count_unique_dates).must_be(:>, 1)
    _(count_unique_dates).must_be(:<, 90)
  end

  it "seeds rates with saved data" do
    Bank.seed_with_saved_data!

    _(count_unique_dates).must_be(:>, 90)
  end

  it "does not duplicate when fetching rates for last 90 days" do
    Bank.fetch_ninety_days!
    count = count_unique_dates
    Bank.fetch_ninety_days!

    _(count_unique_dates).must_equal(count)
  end

  it "fetches current rates" do
    Bank.fetch_current!

    _(count_unique_dates).must_equal(1)
  end

  it "does not duplicate when fetching current rates" do
    2.times { Bank.fetch_current! }

    _(count_unique_dates).must_equal(1)
  end

  it "replaces all rates" do
    Bank.replace_all!

    _(count_unique_dates).must_be(:>, 90)
  end

  it "stores multiple currencies per date" do
    Bank.fetch_current!
    date = Currency.first.date
    currencies_for_date = Currency.where(date: date).count

    _(currencies_for_date).must_be(:>, 1)
  end
end
