# frozen_string_literal: true

require_relative "helper"
require "currency"

describe Currency do
  before do
    Rate.dataset.delete
    Rate.multi_insert([
      { provider: "ECB", date: Date.today, base: "EUR", quote: "USD", rate: 1.1 },
      { provider: "ECB", date: Date.today, base: "EUR", quote: "GBP", rate: 0.85 },
      { provider: "BOC", date: Date.today, base: "CAD", quote: "USD", rate: 0.74 },
      { provider: "ECB", date: Date.today - 365, base: "EUR", quote: "SEK", rate: 11.0 },
    ])

    db = Sequel::Model.db
    db[:currencies].delete
    db.run(<<~SQL)
      INSERT OR REPLACE INTO currencies (iso_code, start_date, end_date)
      SELECT iso_code, MIN(start_date), MAX(end_date)
      FROM (
        SELECT quote AS iso_code, MIN(date) AS start_date, MAX(date) AS end_date
        FROM rates GROUP BY quote
        UNION ALL
        SELECT base AS iso_code, MIN(date) AS start_date, MAX(date) AS end_date
        FROM rates GROUP BY base
      )
      GROUP BY iso_code
      ORDER BY iso_code
    SQL

    db[:currency_coverages].delete
    db.run(<<~SQL)
      INSERT OR REPLACE INTO currency_coverages (provider_key, iso_code, start_date, end_date)
      SELECT provider, iso_code, MIN(date), MAX(date)
      FROM (
        SELECT provider, quote AS iso_code, date FROM rates
        UNION ALL
        SELECT provider, base AS iso_code, date FROM rates
      )
      GROUP BY provider, iso_code
      ORDER BY provider, iso_code
    SQL
  end

  it "lists all currencies" do
    codes = Currency.all.map(&:iso_code).sort

    _(codes).must_include("USD")
    _(codes).must_include("EUR")
    _(codes).must_include("CAD")
  end

  it "merges date ranges across quote and base" do
    usd = Currency.find("USD")

    _(usd).wont_be_nil
    _(usd.start_date.to_s).must_equal(Date.today.to_s)
    _(usd.end_date.to_s).must_equal(Date.today.to_s)
  end

  it "includes base currencies" do
    eur = Currency.find("EUR")

    _(eur).wont_be_nil
  end

  it "filters active currencies" do
    active_codes = Currency.active.map(&:iso_code)

    _(active_codes).must_include("USD")
    _(active_codes).wont_include("SEK")
  end

  it "returns nil for unknown currency" do
    _(Currency.find("XYZ")).must_be_nil
  end

  it "formats to hash" do
    usd = Currency.find("USD")

    _(usd.to_h[:name]).must_equal("United States Dollar")
    _(usd.to_h[:symbol]).must_equal("$")
    _(usd.to_h[:iso_numeric]).must_equal("840")
  end

  it "includes providers" do
    usd = Currency.find("USD")

    _(usd.providers).must_include("ECB")
    _(usd.providers).must_include("BOC")
  end

  it "is case insensitive" do
    _(Currency.find("usd")).wont_be_nil
  end

  it "filters by providers" do
    codes = Currency.with_providers(["ECB"]).map(&:iso_code)

    _(codes).must_include("USD")
    _(codes).must_include("EUR")
    _(codes).wont_include("CAD")
  end

  it "excludes pegged currencies when filtering by providers" do
    codes = Currency.with_providers(["ECB"]).map(&:iso_code)

    _(codes).wont_include("BMD")
  end

  it "includes pegged currencies in the list" do
    codes = Currency.all.map(&:iso_code)

    _(codes).must_include("BMD")
    _(codes).must_include("FKP")
  end

  it "derives pegged currency date range from anchor" do
    bmd = Currency.find("BMD")

    _(bmd).wont_be_nil
    usd = Currency.find("USD")

    _(bmd.end_date.to_s).must_equal(usd.end_date.to_s)
  end

  it "uses later of peg since and anchor start_date" do
    bmd = Currency.find("BMD")
    usd = Currency.find("USD")

    _(bmd.start_date.to_s).must_be(:>=, usd.start_date.to_s)
  end

  it "extends start_date back to peg start when provider data is newer" do
    # AED is pegged to USD since 1997-11-02. Insert provider data starting
    # much later, but anchor (USD) data going back further.
    db = Sequel::Model.db
    Rate.multi_insert([
      { provider: "ECB", date: "1990-01-02", base: "EUR", quote: "USD", rate: 1.0 },
      { provider: "TCMB", date: Date.today, base: "USD", quote: "AED", rate: 3.6725 },
    ])
    db[:currencies].insert_conflict(:replace).insert(iso_code: "USD", start_date: "1990-01-02", end_date: Date.today.to_s)
    db[:currencies].insert_conflict(:replace).insert(iso_code: "AED", start_date: Date.today.to_s, end_date: Date.today.to_s)
    db[:currency_coverages].insert_conflict(:replace).insert(provider_key: "TCMB", iso_code: "AED", start_date: Date.today.to_s, end_date: Date.today.to_s)

    aed = Currency.find("AED")

    _(aed.start_date.to_s).must_equal("1997-11-02")
    _(aed.peg).wont_be_nil
    _(aed.providers).must_include("TCMB")
  end

  it "extends start_date in currency list for pegged currencies with provider data" do
    db = Sequel::Model.db
    Rate.multi_insert([
      { provider: "ECB", date: "1990-01-02", base: "EUR", quote: "USD", rate: 1.0 },
      { provider: "TCMB", date: Date.today, base: "USD", quote: "AED", rate: 3.6725 },
    ])
    db[:currencies].insert_conflict(:replace).insert(iso_code: "USD", start_date: "1990-01-02", end_date: Date.today.to_s)
    db[:currencies].insert_conflict(:replace).insert(iso_code: "AED", start_date: Date.today.to_s, end_date: Date.today.to_s)
    db[:currency_coverages].insert_conflict(:replace).insert(provider_key: "TCMB", iso_code: "AED", start_date: Date.today.to_s, end_date: Date.today.to_s)

    aed = Currency.all.find { |c| c.iso_code == "AED" }

    _(aed.start_date.to_s).must_equal("1997-11-02")
  end

  it "formats pegged currency to hash" do
    bmd = Currency.find("BMD")

    _(bmd.to_h[:iso_code]).must_equal("BMD")
    _(bmd.to_h[:name]).must_equal("Bermudian Dollar")
    _(bmd.to_h[:start_date]).wont_be_nil
    _(bmd.to_h[:end_date]).wont_be_nil
  end

  it "returns peg metadata for pegged currencies" do
    h = Currency.find("BMD").to_h_with_providers

    _(h[:peg][:base]).must_equal("USD")
    _(h[:peg][:rate]).must_equal(1.0)
    _(h[:peg][:authority]).must_equal("Bermuda Monetary Authority")
  end

  it "returns providers for pegged currencies" do
    h = Currency.find("BMD").to_h_with_providers

    _(h[:providers]).must_be_kind_of(Array)
  end

  it "returns providers for non-pegged currencies" do
    h = Currency.find("USD").to_h_with_providers

    _(h[:providers]).must_be_kind_of(Array)
  end

  it "does not return peg metadata for non-pegged currencies" do
    h = Currency.find("USD").to_h_with_providers

    _(h).wont_include(:peg)
  end
end
