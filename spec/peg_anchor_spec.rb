# frozen_string_literal: true

require_relative "helper"
require "peg_anchor"
require "peg"

describe PegAnchor do
  let(:date) { Date.parse("2024-01-15") }

  describe "matched-base peg substitution" do
    it "uses the peg rate when request base matches the peg's base" do
      blended = [
        { date:, base: "USD", quote: "AED", rate: 3.67, providers: ["ECB"] },
      ]

      result = PegAnchor.apply(blended, base: "USD", base_peg: nil)
      aed = result.find { |r| r[:quote] == "AED" }

      _(aed[:rate]).must_equal(3.6725)
    end

    it "omits providers field on peg-substituted rows" do
      blended = [
        { date:, base: "USD", quote: "AED", rate: 3.67, providers: ["ECB"] },
      ]

      result = PegAnchor.apply(blended, base: "USD", base_peg: nil)
      aed = result.find { |r| r[:quote] == "AED" }

      _(aed.key?(:providers)).must_equal(false)
    end
  end

  describe "cross-base peg substitution" do
    it "anchors cross-base pegged quote through the peg's base" do
      blended = [
        { date:, base: "EUR", quote: "USD", rate: 1.10, providers: ["ECB"] },
        { date:, base: "EUR", quote: "AED", rate: 4.04, providers: ["ECB"] },
      ]

      result = PegAnchor.apply(blended, base: "EUR", base_peg: nil)
      aed = result.find { |r| r[:quote] == "AED" }

      _(aed[:rate]).must_be_close_to(1.10 * 3.6725, 0.0001)
    end
  end

  describe "peg-to-peg cross rates" do
    it "computes pure peg cross-rate when both base and quote are pegged" do
      blended = [
        { date:, base: "USD", quote: "EUR", rate: 0.91, providers: ["ECB"] },
      ]

      result = PegAnchor.apply(blended, base: "AED", base_peg: Peg.find("AED"))
      sar = result.find { |r| r[:quote] == "SAR" }

      _(sar[:rate]).must_be_close_to(3.75 / 3.6725, 0.0001)
    end
  end

  describe "base_peg row" do
    it "injects the peg base row when request base is pegged" do
      blended = [
        { date:, base: "USD", quote: "EUR", rate: 0.91, providers: ["ECB"] },
      ]

      result = PegAnchor.apply(blended, base: "AED", base_peg: Peg.find("AED"))
      usd = result.find { |r| r[:quote] == "USD" }

      _(usd[:rate]).must_be_close_to(1.0 / 3.6725, 0.0001)
      _(usd.key?(:providers)).must_equal(false)
    end
  end

  describe "synthesis of peg-only currencies" do
    it "synthesizes rows for currencies providers do not cover" do
      blended = [
        { date:, base: "EUR", quote: "GBP", rate: 0.86, providers: ["ECB"] },
      ]

      result = PegAnchor.apply(blended, base: "EUR", base_peg: nil)
      fkp = result.find { |r| r[:quote] == "FKP" }

      _(fkp).wont_be_nil
      _(fkp[:rate]).must_be_close_to(0.86, 0.0001)
      _(fkp.key?(:providers)).must_equal(false)
    end

    it "skips synthesized rows before peg start date" do
      old_date = Date.parse("1900-01-01")
      blended = [
        { date: old_date, base: "EUR", quote: "GBP", rate: 0.86, providers: ["ECB"] },
      ]

      result = PegAnchor.apply(blended, base: "EUR", base_peg: nil)
      fkp = result.find { |r| r[:quote] == "FKP" }

      _(fkp).must_be_nil
    end
  end

  describe "empty input" do
    it "returns an empty array when no rates are provided" do
      result = PegAnchor.apply([], base: "EUR", base_peg: nil)

      _(result).must_equal([])
    end
  end
end
