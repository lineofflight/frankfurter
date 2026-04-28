# frozen_string_literal: true

require_relative "helper"
require "peg_anchor"

describe PegAnchor do
  let(:date) { Date.parse("2024-01-15") }

  describe "matched-base peg substitution" do
    it "uses the peg rate when request base matches the peg's base" do
      rates = [
        { date:, base: "EUR", quote: "USD", rate: 1.08, provider: "ECB" },
        { date:, base: "EUR", quote: "AED", rate: 99.0, provider: "ECB" },
      ]

      result = PegAnchor.new(rates, base: "USD").blend
      aed = result.find { |r| r[:quote] == "AED" }

      _(aed[:rate]).must_equal(3.6725)
    end

    it "omits providers field on peg-substituted rows" do
      rates = [
        { date:, base: "EUR", quote: "USD", rate: 1.08, provider: "ECB" },
        { date:, base: "EUR", quote: "AED", rate: 99.0, provider: "ECB" },
      ]

      result = PegAnchor.new(rates, base: "USD").blend
      aed = result.find { |r| r[:quote] == "AED" }

      _(aed.key?(:providers)).must_equal(false)
    end
  end

  describe "cross-base peg substitution" do
    it "anchors cross-base pegged quote through the peg's base" do
      rates = [
        { date:, base: "EUR", quote: "USD", rate: 1.10, provider: "ECB" },
        { date:, base: "EUR", quote: "AED", rate: 99.0, provider: "ECB" },
      ]

      result = PegAnchor.new(rates, base: "EUR").blend
      aed = result.find { |r| r[:quote] == "AED" }
      usd = result.find { |r| r[:quote] == "USD" }

      _(aed[:rate]).must_be_close_to(usd[:rate] * 3.6725, 0.0001)
    end

    it "leaves the bridge currency's providers field intact" do
      rates = [
        { date:, base: "EUR", quote: "USD", rate: 1.10, provider: "ECB" },
        { date:, base: "EUR", quote: "AED", rate: 99.0, provider: "ECB" },
      ]

      result = PegAnchor.new(rates, base: "EUR").blend
      usd = result.find { |r| r[:quote] == "USD" }

      _(usd[:providers]).must_equal(["ECB"])
    end
  end

  describe "peg-to-peg cross rates" do
    it "computes pure peg cross-rate when both base and quote are pegged" do
      rates = [
        { date:, base: "EUR", quote: "USD", rate: 1.10, provider: "ECB" },
      ]

      result = PegAnchor.new(rates, base: "AED").blend
      sar = result.find { |r| r[:quote] == "SAR" }

      _(sar[:rate]).must_be_close_to(3.75 / 3.6725, 0.0001)
    end
  end

  describe "base_peg row" do
    it "injects the peg base row when request base is pegged" do
      rates = [
        { date:, base: "EUR", quote: "USD", rate: 1.10, provider: "ECB" },
      ]

      result = PegAnchor.new(rates, base: "AED").blend
      usd = result.find { |r| r[:quote] == "USD" }

      _(usd[:rate]).must_be_close_to(1.0 / 3.6725, 0.0001)
      _(usd.key?(:providers)).must_equal(false)
    end
  end

  describe "synthesis of peg-only currencies" do
    it "synthesizes rows for currencies providers do not cover" do
      rates = [
        { date:, base: "EUR", quote: "GBP", rate: 0.86, provider: "ECB" },
      ]

      result = PegAnchor.new(rates, base: "EUR").blend
      fkp = result.find { |r| r[:quote] == "FKP" }

      _(fkp).wont_be_nil
      _(fkp[:rate]).must_be_close_to(0.86, 0.0001)
      _(fkp.key?(:providers)).must_equal(false)
    end

    it "skips synthesized rows before peg start date" do
      old_date = Date.parse("1900-01-01")
      rates = [
        { date: old_date, base: "EUR", quote: "GBP", rate: 0.86, provider: "ECB" },
      ]

      result = PegAnchor.new(rates, base: "EUR").blend
      fkp = result.find { |r| r[:quote] == "FKP" }

      _(fkp).must_be_nil
    end
  end

  describe "empty input" do
    it "returns an empty array when no rates are provided" do
      result = PegAnchor.new([], base: "EUR").blend

      _(result).must_equal([])
    end
  end
end
