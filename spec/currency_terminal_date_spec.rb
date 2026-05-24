# frozen_string_literal: true

require_relative "helper"
require "currency_terminal_date"

describe CurrencyTerminalDate do
  it "loads all entries" do
    _(CurrencyTerminalDate.all).wont_be_empty
  end

  it "returns a frozen array" do
    _(CurrencyTerminalDate.all).must_be(:frozen?)
  end

  it "parses every terminal_date as a Date" do
    CurrencyTerminalDate.all.each do |entry|
      _(entry.terminal_date).must_be_instance_of(Date)
    end
  end

  it "requires iso_code, terminal_date, and source" do
    CurrencyTerminalDate.all.each do |entry|
      _(entry.iso_code).wont_be_nil
      _(entry.iso_code).must_match(/\A[A-Z]{3}\z/)
      _(entry.source).wont_be_nil
      _(entry.source).must_match(%r{\Ahttps?://})
    end
  end

  it "covers the known defunct codes" do
    codes = CurrencyTerminalDate.all.map(&:iso_code)

    ["BGN", "BYR", "EEK", "HRK", "IEP", "SLL", "STD", "VEF", "ZMK"].each do |code|
      _(codes).must_include(code)
    end
  end

  it "looks up an entry by iso_code" do
    entry = CurrencyTerminalDate.find("BYR")

    _(entry).wont_be_nil
    _(entry.terminal_date).must_equal(Date.new(2016, 7, 1))
    _(entry.successor).must_equal("BYN")
    _(entry.ratio).must_equal(10000)
  end

  it "returns nil for an unknown iso_code" do
    _(CurrencyTerminalDate.find("USD")).must_be_nil
  end

  describe ".purge" do
    let(:db) { Sequel::Model.db }

    it "deletes rates dated on or after the terminal date and keeps earlier rows" do
      db[:rates].multi_insert([
        # BYR (terminal 2016-07-01) — these go
        { provider: "TEST", date: Date.new(2016, 7, 1), base: "USD", quote: "BYR", rate: 22000.0 },
        { provider: "TEST", date: Date.new(2017, 1, 1), base: "BYR", quote: "USD", rate: 0.00005 },
        # BYR before terminal date — kept
        { provider: "TEST", date: Date.new(2016, 6, 30), base: "USD", quote: "BYR", rate: 22000.0 },
        # Unrelated rate — kept
        { provider: "TEST", date: Date.new(2016, 7, 1), base: "EUR", quote: "USD", rate: 1.1 },
      ])

      totals = CurrencyTerminalDate.purge(db)

      _(totals[:rates]).must_equal(2)
      _(db[:rates].where(quote: "BYR", date: Date.new(2016, 7, 1)).count).must_equal(0)
      _(db[:rates].where(base: "BYR", date: Date.new(2017, 1, 1)).count).must_equal(0)
      _(db[:rates].where(quote: "BYR", date: Date.new(2016, 6, 30)).count).must_equal(1)
      _(db[:rates].where(base: "EUR", date: Date.new(2016, 7, 1)).count).must_equal(1)
    end

    it "removes rollup rows past the terminal date" do
      db[:weekly_rates].multi_insert([
        { provider: "TEST", bucket_date: Date.new(2016, 7, 4), base: "USD", quote: "BYR", rate: 22000.0 },
        { provider: "TEST", bucket_date: Date.new(2016, 6, 27), base: "USD", quote: "BYR", rate: 22000.0 },
      ])
      db[:monthly_rates].multi_insert([
        { provider: "TEST", bucket_date: Date.new(2016, 8, 1), base: "USD", quote: "BYR", rate: 22000.0 },
        { provider: "TEST", bucket_date: Date.new(2016, 6, 1), base: "USD", quote: "BYR", rate: 22000.0 },
      ])

      totals = CurrencyTerminalDate.purge(db)

      _(totals[:weekly_rates]).must_equal(1)
      _(totals[:monthly_rates]).must_equal(1)
      _(db[:weekly_rates].where(quote: "BYR", bucket_date: Date.new(2016, 6, 27)).count).must_equal(1)
      _(db[:monthly_rates].where(quote: "BYR", bucket_date: Date.new(2016, 6, 1)).count).must_equal(1)
    end

    it "refreshes currency summaries for affected codes" do
      db[:rates].multi_insert([
        { provider: "TEST", date: Date.new(2016, 6, 30), base: "USD", quote: "BYR", rate: 22000.0 },
        { provider: "TEST", date: Date.new(2017, 1, 1), base: "USD", quote: "BYR", rate: 22000.0 },
      ])
      db[:currencies].where(iso_code: "BYR").delete
      db[:currencies].insert(iso_code: "BYR", start_date: "2016-06-30", end_date: "2017-01-01")
      db[:currency_coverages].where(iso_code: "BYR").delete
      db[:currency_coverages].insert(
        provider_key: "TEST",
        iso_code: "BYR",
        start_date: "2016-06-30",
        end_date: "2017-01-01",
      )

      CurrencyTerminalDate.purge(db)

      currency = db[:currencies].where(iso_code: "BYR").first
      coverage = db[:currency_coverages].where(iso_code: "BYR", provider_key: "TEST").first

      _(currency[:end_date].to_s).must_equal("2016-06-30")
      _(coverage[:end_date].to_s).must_equal("2016-06-30")
    end

    it "removes currency rows entirely when no surviving rates remain" do
      db[:rates].insert(
        provider: "TEST",
        date: Date.new(2017, 1, 1),
        base: "USD",
        quote: "BYR",
        rate: 22000.0,
      )
      db[:currencies].where(iso_code: "BYR").delete
      db[:currencies].insert(iso_code: "BYR", start_date: "2017-01-01", end_date: "2017-01-01")
      db[:currency_coverages].where(iso_code: "BYR").delete
      db[:currency_coverages].insert(
        provider_key: "TEST",
        iso_code: "BYR",
        start_date: "2017-01-01",
        end_date: "2017-01-01",
      )

      CurrencyTerminalDate.purge(db)

      _(db[:currencies].where(iso_code: "BYR").count).must_equal(0)
      _(db[:currency_coverages].where(iso_code: "BYR").count).must_equal(0)
    end
  end

  describe ".expired?" do
    it "returns true for a date on the terminal date" do
      _(CurrencyTerminalDate.expired?("BYR", Date.new(2016, 7, 1))).must_equal(true)
    end

    it "returns true for a date after the terminal date" do
      _(CurrencyTerminalDate.expired?("BYR", Date.new(2017, 1, 1))).must_equal(true)
    end

    it "returns false for a date before the terminal date" do
      _(CurrencyTerminalDate.expired?("BYR", Date.new(2016, 6, 30))).must_equal(false)
    end

    it "returns false for a code not in the table" do
      _(CurrencyTerminalDate.expired?("USD", Date.new(2030, 1, 1))).must_equal(false)
    end
  end
end
