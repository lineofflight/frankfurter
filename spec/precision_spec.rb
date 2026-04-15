# frozen_string_literal: true

require_relative "helper"
require "precision"

describe Precision do
  describe ".significant_digits" do
    it "counts digits in a typical rate" do
      _(Precision.significant_digits(1.0836)).must_equal(5)
    end

    it "counts digits in a large rate" do
      _(Precision.significant_digits(19604.0)).must_equal(5)
    end

    it "counts digits in a small rate" do
      _(Precision.significant_digits(0.006142)).must_equal(4)
    end

    it "counts digits in an integer-like rate" do
      _(Precision.significant_digits(160.0)).must_equal(3)
    end

    it "counts digits in a high-precision rate" do
      _(Precision.significant_digits(107.3421)).must_equal(7)
    end
  end

  describe ".derive" do
    it "returns median significant digits per quote" do
      rates = [
        { base: "EUR", quote: "INR", rate: 107.3, provider: "A" },
        { base: "EUR", quote: "INR", rate: 107.3421, provider: "B" },
        { base: "EUR", quote: "INR", rate: 107.34, provider: "C" },
      ]

      result = Precision.derive(rates)
      _(result["INR"]).must_equal(5) # median of [4, 7, 5] = 5
    end

    it "handles a single provider" do
      rates = [
        { base: "EUR", quote: "USD", rate: 1.0836, provider: "ECB" },
      ]

      result = Precision.derive(rates)
      _(result["USD"]).must_equal(5)
    end

    it "groups by quote currency across different bases" do
      rates = [
        { base: "EUR", quote: "CHF", rate: 0.9213, provider: "ECB" },
        { base: "JPY", quote: "CHF", rate: 0.006142, provider: "BOJ" },
      ]

      result = Precision.derive(rates)
      _(result["CHF"]).must_equal(4) # median of [4, 4] = 4
    end
  end

  describe ".decimal_places" do
    it "converts sig digits for a value around 100" do
      _(Precision.decimal_places(5, 107.34)).must_equal(2)
    end

    it "converts sig digits for a value around 20000" do
      _(Precision.decimal_places(5, 19604.0)).must_equal(0)
    end

    it "converts sig digits for a value less than 1" do
      _(Precision.decimal_places(4, 0.92)).must_equal(4)
    end

    it "clamps to zero" do
      _(Precision.decimal_places(2, 19604.0)).must_equal(0)
    end
  end
end
