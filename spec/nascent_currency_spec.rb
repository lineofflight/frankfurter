# frozen_string_literal: true

require_relative "helper"
require "nascent_currency"

describe NascentCurrency do
  it "loads all entries" do
    _(NascentCurrency.all).wont_be_empty
  end

  it "returns a frozen array" do
    _(NascentCurrency.all).must_be(:frozen?)
  end

  it "parses every inception_date as a Date" do
    NascentCurrency.all.each do |entry|
      _(entry.inception_date).must_be_instance_of(Date)
    end
  end

  it "requires iso_code, inception_date, and source" do
    NascentCurrency.all.each do |entry|
      _(entry.iso_code).wont_be_nil
      _(entry.iso_code).must_match(/\A[A-Z]{3}\z/)
      _(entry.source).wont_be_nil
      _(entry.source).must_match(%r{\Ahttps?://})
    end
  end

  it "covers the euro" do
    _(NascentCurrency.all.map(&:iso_code)).must_include("EUR")
  end

  it "looks up an entry by iso_code" do
    entry = NascentCurrency.find("EUR")

    _(entry).wont_be_nil
    _(entry.inception_date).must_equal(Date.new(1999, 1, 4))
    _(entry.predecessor).must_equal("XEU")
  end

  it "returns nil for an unknown iso_code" do
    _(NascentCurrency.find("USD")).must_be_nil
  end

  describe ".premature?" do
    it "returns true for a date before the inception date" do
      _(NascentCurrency.premature?("EUR", Date.new(1998, 12, 31))).must_equal(true)
    end

    it "returns false for a date on the inception date" do
      _(NascentCurrency.premature?("EUR", Date.new(1999, 1, 4))).must_equal(false)
    end

    it "returns false for a date after the inception date" do
      _(NascentCurrency.premature?("EUR", Date.new(2020, 1, 1))).must_equal(false)
    end

    it "returns false for a code not in the table" do
      _(NascentCurrency.premature?("USD", Date.new(1900, 1, 1))).must_equal(false)
    end
  end
end
