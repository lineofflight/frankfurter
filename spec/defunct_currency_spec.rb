# frozen_string_literal: true

require_relative "helper"
require "defunct_currency"

describe DefunctCurrency do
  it "loads all entries" do
    _(DefunctCurrency.all).wont_be_empty
  end

  it "returns a frozen array" do
    _(DefunctCurrency.all).must_be(:frozen?)
  end

  it "parses every terminal_date as a Date" do
    DefunctCurrency.all.each do |entry|
      _(entry.terminal_date).must_be_instance_of(Date)
    end
  end

  it "requires iso_code, terminal_date, and source" do
    DefunctCurrency.all.each do |entry|
      _(entry.iso_code).wont_be_nil
      _(entry.iso_code).must_match(/\A[A-Z]{3}\z/)
      _(entry.source).wont_be_nil
      _(entry.source).must_match(%r{\Ahttps?://})
    end
  end

  it "covers the known defunct codes" do
    codes = DefunctCurrency.all.map(&:iso_code)

    ["ADP", "AFA", "ATS", "BEF", "BGL", "BGN", "BYB", "BYR", "CUC", "DEM", "ECS", "EEK", "ESP", "FRF", "HRK", "IEP",
     "ITL", "MGF", "MZM", "NLG", "PTE", "SDD", "SLL", "SRG", "STD", "VEB", "VEF", "ZMK",].each do |code|
      _(codes).must_include(code)
    end
  end

  it "looks up an entry by iso_code" do
    entry = DefunctCurrency.find("BYR")

    _(entry).wont_be_nil
    _(entry.terminal_date).must_equal(Date.new(2016, 7, 1))
    _(entry.successor).must_equal("BYN")
    _(entry.ratio).must_equal(10000)
  end

  it "retires the Ecuadorian sucre after the 180-day withdrawal period" do
    entry = DefunctCurrency.find("ECS")

    _(entry.terminal_date).must_equal(Date.new(2000, 9, 9))
    _(entry.successor).must_equal("USD")
    _(entry.ratio).must_equal(25000)
  end

  it "retires redenominated legacy codes on their changeover dates" do
    { "BGL" => [Date.new(1999, 7, 5), "BGN"], "MZM" => [Date.new(2006, 7, 1), "MZN"],
      "VEB" => [Date.new(2008, 1, 1), "VEF"], }.each do |code, (terminal_date, successor)|
      entry = DefunctCurrency.find(code)

      _(entry.terminal_date).must_equal(terminal_date)
      _(entry.successor).must_equal(successor)
      _(entry.ratio).must_equal(1000)
    end
  end

  it "returns nil for an unknown iso_code" do
    _(DefunctCurrency.find("USD")).must_be_nil
  end

  describe ".expired?" do
    it "returns true for a date on the terminal date" do
      _(DefunctCurrency.expired?("BYR", Date.new(2016, 7, 1))).must_equal(true)
    end

    it "returns true for a date after the terminal date" do
      _(DefunctCurrency.expired?("BYR", Date.new(2017, 1, 1))).must_equal(true)
    end

    it "returns false for a date before the terminal date" do
      _(DefunctCurrency.expired?("BYR", Date.new(2016, 6, 30))).must_equal(false)
    end

    it "returns false for a code not in the table" do
      _(DefunctCurrency.expired?("USD", Date.new(2030, 1, 1))).must_equal(false)
    end
  end
end
