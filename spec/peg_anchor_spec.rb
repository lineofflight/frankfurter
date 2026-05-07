# frozen_string_literal: true

require_relative "helper"
require "peg_anchor"
require "peg"

describe PegAnchor do
  let(:date) { Date.parse("2024-01-15") }

  describe "matched-base peg substitution" do
    it "uses the peg rate when request base matches the peg's base" do
      blended = [
        { date:, base: "USD", quote: "AED", rate: 3.67, providers: [{ key: "ECB", rate: 0.0 }] },
      ]

      result = PegAnchor.apply(blended, base: "USD")
      aed = result.find { |r| r[:quote] == "AED" }

      _(aed[:rate]).must_equal(3.6725)
    end

    it "preserves providers on peg-substituted rows, marking all excluded" do
      blended = [
        { date:, base: "USD", quote: "AED", rate: 3.67, providers: [{ key: "ECB", rate: 3.67 }] },
      ]

      result = PegAnchor.apply(blended, base: "USD")
      aed = result.find { |r| r[:quote] == "AED" }

      _(aed[:providers]).must_equal([{ key: "ECB", rate: 3.67, excluded: true }])
    end
  end

  describe "cross-base peg substitution" do
    it "anchors cross-base pegged quote through the peg's base" do
      blended = [
        { date:, base: "EUR", quote: "USD", rate: 1.10, providers: [{ key: "ECB", rate: 0.0 }] },
        { date:, base: "EUR", quote: "AED", rate: 4.04, providers: [{ key: "ECB", rate: 0.0 }] },
      ]

      result = PegAnchor.apply(blended, base: "EUR")
      aed = result.find { |r| r[:quote] == "AED" }

      _(aed[:rate]).must_be_close_to(1.10 * 3.6725, 0.0001)
    end
  end

  describe "synthesis of peg-only currencies" do
    it "synthesizes rows for currencies providers do not cover" do
      blended = [
        { date:, base: "EUR", quote: "GBP", rate: 0.86, providers: [{ key: "ECB", rate: 0.86 }] },
      ]

      result = PegAnchor.apply(blended, base: "EUR")
      fkp = result.find { |r| r[:quote] == "FKP" }

      _(fkp).wont_be_nil
      _(fkp[:rate]).must_be_close_to(0.86, 0.0001)
      _(fkp.key?(:providers)).must_equal(false)
    end

    it "skips synthesized rows before peg start date" do
      old_date = Date.parse("1900-01-01")
      blended = [
        { date: old_date, base: "EUR", quote: "GBP", rate: 0.86, providers: [{ key: "ECB", rate: 0.0 }] },
      ]

      result = PegAnchor.apply(blended, base: "EUR")
      fkp = result.find { |r| r[:quote] == "FKP" }

      _(fkp).must_be_nil
    end
  end

  describe "empty input" do
    it "returns an empty array when no rates are provided" do
      result = PegAnchor.apply([], base: "EUR")

      _(result).must_equal([])
    end
  end
end
