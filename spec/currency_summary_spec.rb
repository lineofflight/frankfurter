# frozen_string_literal: true

require_relative "helper"
require "provider"
require "rate_validation"

tables = [:rates, :currencies, :currency_coverages]
terminal_codes = ["USD", "BYR"]
["provider", "purge"].each do |path|
  describe "Currency summaries via #{path}" do
    define_method(:refresh_summaries) do |codes|
      if path == "provider"
        Provider["ECB"].send(:refresh_currency_summaries, codes)
      else
        RateValidation.send(:rebuild_summaries, DB, codes)
      end
    end

    before do
      tables.each { |table| DB[table].delete }
      DB[:currency_exclusions].delete if DB.table_exists?(:currency_exclusions)
    end

    it "keeps unknown codes out of both catalogues without extending their counterpart's coverage" do
      DB[:rates].multi_insert([
        { provider: "ECB", date: "2000-01-03", base: "USD", quote: "EUR", mid: 0.9 },
        { provider: "ECB", date: "2000-01-04", base: "USD", quote: "SDR", mid: 3.0 },
        { provider: "ECB", date: "2000-01-05", base: "SDR", quote: "USD", mid: 0.3 },
      ])
      refresh_summaries(["USD", "EUR", "SDR"])

      _(DB[:currencies].where(iso_code: "SDR").count).must_equal(0)
      _(DB[:currency_coverages].where(iso_code: "SDR").count).must_equal(0)
      _(DB[:currencies].where(iso_code: "USD").get(:end_date).to_s).must_equal("2000-01-03")
      _(DB[:currency_coverages].where(iso_code: "USD").get(:end_date).to_s).must_equal("2000-01-03")
    end

    it "stores the unknown code's publication dates separately" do
      _(DB.table_exists?(:currency_exclusions)).must_equal(true)
      DB[:rates].multi_insert([
        { provider: "ECB", date: "2000-01-04", base: "USD", quote: "SDR", mid: 3.0 },
        { provider: "ECB", date: "2000-01-05", base: "SDR", quote: "USD", mid: 0.3 },
      ])
      refresh_summaries(["SDR", "USD"])
      row = DB[:currency_exclusions].where(provider_key: "ECB", iso_code: "SDR").first

      _(row[:start_date].to_s).must_equal("2000-01-04")
      _(row[:end_date].to_s).must_equal("2000-01-05")
      _(Provider["ECB"].currency_exclusions.map(&:iso_code)).must_equal(["SDR"])
    end

    it "removes exclusions once the currency can be named" do
      _(DB.table_exists?(:currency_exclusions)).must_equal(true)
      DB[:rates].insert(provider: "ECB", date: "2000-01-04", base: "USD", quote: "EUR", mid: 0.9)
      DB[:currency_exclusions].insert(
        provider_key: "ECB", iso_code: "EUR", start_date: "2000-01-04", end_date: "2000-01-04",
      )
      refresh_summaries(["EUR"])

      _(DB[:currency_exclusions].where(iso_code: "EUR").count).must_equal(0)
      _(DB[:currencies].where(iso_code: "EUR").count).must_equal(1)
    end

    it "keeps terminal dates exclusive without advertising post-terminal counterparts" do
      DB[:rates].multi_insert([
        { provider: "ECB", date: "2016-06-29", base: "USD", quote: "BYR", mid: 20000.0 },
        { provider: "ECB", date: "2016-06-30", base: "USD", quote: "BYR", mid: 21000.0 },
        { provider: "ECB", date: "2016-07-01", base: "USD", quote: "BYR", mid: 22000.0 },
        { provider: "ECB", date: "2017-01-01", base: "BYR", quote: "USD", mid: 0.00004 },
      ])
      DB[:currencies].insert(iso_code: "BYR", start_date: "2016-06-29", end_date: "2017-01-01")
      DB[:currency_coverages].insert(
        provider_key: "ECB", iso_code: "BYR", start_date: "2016-06-29", end_date: "2017-01-01",
      )
      refresh_summaries(["USD", "BYR"])

      terminal_codes.each do |code|
        _(DB[:currencies].where(iso_code: code).get(:end_date).to_s).must_equal("2016-06-30")
        _(DB[:currency_coverages].where(iso_code: code).get(:end_date).to_s).must_equal("2016-06-30")
      end
    end

    it "omits entirely post-terminal ranges instead of inverting their dates" do
      DB[:rates].insert(provider: "ECB", date: "2017-01-01", base: "BYR", quote: "USD", mid: 0.00004)
      refresh_summaries(["USD", "BYR"])

      _(DB[:currencies].count).must_equal(0)
      _(DB[:currency_coverages].count).must_equal(0)
    end
  end
end

describe "Recognized currency exclusions" do
  it "restores coverage on startup when a previously unknown code can now be named" do
    DB[:rates].delete
    DB[:currency_coverages].delete
    DB[:currencies].delete
    DB[:currency_exclusions].delete
    DB[:rates].insert(provider: "ECB", date: "2000-01-04", base: "USD", quote: "EUR", mid: 0.9)
    DB[:currency_exclusions].insert(
      provider_key: "ECB", iso_code: "EUR", start_date: "2000-01-04", end_date: "2000-01-04",
    )

    Provider.seed

    _(DB[:currency_exclusions].where(iso_code: "EUR").count).must_equal(0)
    _(DB[:currency_coverages].where(provider_key: "ECB", iso_code: "EUR").count).must_equal(1)
    _(DB[:currencies].where(iso_code: "EUR").get(:start_date).to_s).must_equal("2000-01-04")
    _(DB[:currencies].where(iso_code: "USD").get(:start_date).to_s).must_equal("2000-01-04")
  end

  it "clears modern sucre exclusions without advertising coverage" do
    DB[:rates].delete
    DB[:currency_coverages].delete
    DB[:currencies].delete
    DB[:currency_exclusions].delete
    DB[:rates].insert(provider: "CBKKW", date: "2026-09-17", base: "ECS", quote: "KWD", mid: 0.000012)
    DB[:currency_exclusions].insert(
      provider_key: "CBKKW", iso_code: "ECS", start_date: "2026-09-17", end_date: "2026-09-17",
    )

    Provider.seed

    _(DB[:currency_exclusions].where(iso_code: "ECS").count).must_equal(0)
    _(DB[:currency_coverages].where(iso_code: "ECS").count).must_equal(0)
    _(DB[:currencies].where(iso_code: "ECS").count).must_equal(0)
    _(Provider["CBKKW"].unknown_currencies).wont_include("ECS")
  end
end
