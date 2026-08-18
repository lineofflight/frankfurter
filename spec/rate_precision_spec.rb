# frozen_string_literal: true

require_relative "helper"
require "rate_precision"

describe RatePrecision do
  describe ".normalize" do
    it "strips noise from a synthesized midpoint" do
      _(RatePrecision.normalize((181.5264 + 181.76) / 2.0)).must_equal(181.6432)
    end

    it "strips noise from per-unit scaling" do
      _(RatePrecision.normalize(744.92 / 100.0)).must_equal(7.4492)
    end

    it "strips noise from a cross rate" do
      _(RatePrecision.normalize(0.9136230000000001)).must_equal(0.913623)
    end

    it "leaves a clean rate untouched" do
      _(RatePrecision.normalize(1.0876)).must_equal(1.0876)
    end

    it "keeps every digit of a rate published to ten significant digits" do
      _(RatePrecision.normalize(0.0001234567891)).must_equal(0.0001234567891)
    end

    it "keeps the magnitude of very large rates" do
      _(RatePrecision.normalize(260988505.32818)).must_equal(260988505.328)
    end

    it "keeps the magnitude of very small rates" do
      _(RatePrecision.normalize(1.2e-9)).must_equal(1.2e-9)
    end

    it "round-trips through a double without loss" do
      value = RatePrecision.normalize(0.9136230000000001)

      _(Float(value.to_s)).must_equal(value)
    end

    it "passes integers through" do
      _(RatePrecision.normalize(3)).must_equal(3)
    end

    it "passes nil through" do
      _(RatePrecision.normalize(nil)).must_be_nil
    end

    it "passes non-finite values through" do
      _(RatePrecision.normalize(Float::INFINITY)).must_equal(Float::INFINITY)
      _(RatePrecision.normalize(Float::NAN).nan?).must_equal(true)
    end
  end

  describe ".sql" do
    it "strips the same noise SQLite-side" do
      db = Sequel::Model.db
      noisy = (181.5264 + 181.76) / 2.0

      _(db.get(RatePrecision.sql(noisy))).must_equal(181.6432)
    end
  end
end
