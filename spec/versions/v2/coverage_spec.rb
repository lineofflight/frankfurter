# frozen_string_literal: true

require_relative "../../helper"
require "rack/test"
require "versions/v2"

describe "Queryable history coverage" do
  include Rack::Test::Methods

  let(:app) { Versions::V2.freeze }

  before do
    [:rates, :blended_rates, :currencies, :currency_coverages, :currency_exclusions].each { |table| DB[table].delete }
  end

  def observation(provider, date, base, quote, mid = 1.2)
    Rate.dataset.insert(provider:, date:, base:, quote:, mid:)
  end

  def coverage(params = {})
    get "/coverage", params

    _(last_response.status).must_equal(200)
    Oj.load(last_response.body)
  end

  def assert_bounds(params, first, last = first)
    result = coverage(params)

    _(result.values_at("start_date", "end_date")).must_equal([first, last])
    [first, last].compact.uniq.each do |date|
      get "/rates", params.merge(date:)

      _(last_response.status).must_equal(200)
      _(Oj.load(last_response.body)).wont_be_empty
    end
  end

  it "separates the default EUR blend from BIS's monthly USD history" do
    observation("BIS", "1900-01-31", "USD", "ZAR", 0.4107)
    observation("JPC", "1901-01-06", "USD", "EUR")
    observation("BOC", "1950-01-03", "USD", "CAD")
    observation("ECB", "1999-01-04", "EUR", "USD")
    CurrencySummary.refresh(DB, ["USD", "ZAR", "EUR", "CAD"])

    # Existing catalogues already distinguish publication sources, but not a query's base.
    _(Currency.with_providers(["BIS"]).where(iso_code: "ZAR").first.start_date.to_s).must_equal("1900-01-31")
    _(Currency.find("USD").start_date.to_s).must_equal("1950-01-03")
    assert_bounds({}, "1999-01-04")
    assert_bounds({ base: "usd" }, "1950-01-03", "1999-01-04")
    assert_bounds({ providers: "bis", base: "usd", quotes: "zar" }, "1900-01-31")
    assert_bounds({ providers: "bis" }, nil)

    get "/rates", date: "1900-01-31"

    _(Oj.load(last_response.body)).must_equal([])
    BlendedRate.rebuild

    _(BlendedRate.where { date < "1950-01-03" }.count).must_equal(0)
    assert_bounds({}, "1999-01-04")
    assert_bounds({ providers: "BIS", base: "USD", quotes: "ZAR" }, "1900-01-31")
  end

  [false, true].each do |stored|
    describe(stored ? "materialized blend" : "live blend") do
      it "starts when both legs exist, not at an earlier carried quote's date" do
        observation("BOC", "2000-01-01", "USD", "JPY")
        observation("ECB", "2000-01-10", "USD", "EUR")
        BlendedRate.rebuild if stored

        assert_bounds({ base: "EUR", quotes: "JPY" }, "2000-01-10")
        assert_bounds({ base: "EUR", quotes: "USD" }, "2000-01-10")
        assert_bounds({ base: "EUR", quotes: "EUR" }, "2000-01-10")
        assert_bounds({ base: "USD", quotes: "USD" }, "2000-01-01", "2000-01-10")
        get "/rates", base: "EUR", quotes: "JPY", date: "2000-01-01"

        _(Oj.load(last_response.body)).must_equal([])
      end

      it "does not infer pair availability from overlapping currency date bounds" do
        observation("ECB", "2000-01-01", "USD", "EUR")
        observation("BOC", "2000-02-01", "USD", "JPY")
        observation("ECB", "2000-03-01", "USD", "EUR")
        BlendedRate.rebuild if stored

        assert_bounds({ base: "EUR", quotes: "JPY" }, nil)
        assert_bounds({ base: "EUR", quotes: "JPY,USD" }, "2000-01-01", "2000-03-01")
      end

      it "includes the carry-forward boundary without extending history into empty future days" do
        observation("ECB", "2000-01-01", "USD", "EUR")
        observation("BOC", "2000-01-15", "USD", "JPY")
        observation("BOC", "2000-01-16", "USD", "JPY")
        BlendedRate.rebuild if stored

        assert_bounds({ base: "EUR", quotes: "JPY" }, "2000-01-15")
      end
    end
  end

  it "does not advertise disconnected, unknown, or expired rows as default coverage" do
    observation("ECB", "1980-01-01", "GBP", "JPY")
    observation("ECB", "1981-01-01", "USD", "ZZZ")
    observation("ECB", "2017-01-01", "USD", "BYR")

    assert_bounds({ base: "GBP" }, nil)
    assert_bounds({ base: "USD" }, nil)
    assert_bounds({ providers: "ECB", base: "GBP", quotes: "JPY" }, "1980-01-01")
    assert_bounds({ providers: "ECB", base: "USD", quotes: "ZZZ" }, "1981-01-01")
    assert_bounds({ providers: "ECB", base: "USD", quotes: "BYR" }, "2017-01-01")
  end

  it "keeps coverage stable when materialization omits a disconnected publication date" do
    observation("ECB", "2000-01-01", "USD", "EUR")
    observation("BOC", "2000-01-10", "GBP", "JPY")

    assert_bounds({}, "2000-01-01", "2000-01-10")
    BlendedRate.rebuild

    _(BlendedRate.ready?).must_equal(true)
    assert_bounds({}, "2000-01-01", "2000-01-10")
  end

  it "uses the selected provider's wider carry-forward window" do
    observation("BIS", "2000-01-01", "USD", "EUR")
    observation("BIS", "2000-02-10", "USD", "JPY")
    observation("BIS", "2000-03-01", "USD", "JPY")

    assert_bounds({ providers: "BIS", base: "EUR", quotes: "JPY" }, "2000-02-10")
  end

  it "uses the multi-provider USD bridge instead of combining disconnected native crosses" do
    observation("ECB", "2000-01-01", "EUR", "JPY")
    observation("BOC", "2000-01-01", "CAD", "GBP")

    assert_bounds({ providers: "ECB,BOC", base: "EUR", quotes: "JPY" }, nil)
    observation("ECB", "2000-01-03", "EUR", "USD")

    assert_bounds({ providers: "ECB,BOC", base: "EUR", quotes: "JPY" }, "2000-01-03")
  end

  [false, true].each do |stored|
    it "honors peg inception and provider bypass with #{stored ? "stored" : "live"} rates" do
      observation("BOC", "1997-11-01", "USD", "CAD")
      observation("BOC", "1997-11-03", "USD", "CAD")
      BlendedRate.rebuild if stored

      assert_bounds({ base: "AED", quotes: "CAD" }, "1997-11-03")
      assert_bounds({ base: "USD", quotes: "AED" }, "1997-11-03")
      assert_bounds({ base: "USD", quotes: "AED", providers: "BOC" }, nil)
      assert_bounds({ base: "AED", providers: "BOC,ECB" }, nil)
    end
  end

  it "releases the compute slot when the coverage deadline expires" do
    observation("ECB", "2000-01-01", "EUR", "USD")
    query = Versions::V2::RateQuery.new({}, -1)

    Versions::V2::RateQuery.stub(:heavy_slots, HeavySlots.new(1)) do
      _(-> { query.coverage }).must_raise(RequestTimeout::Error)
      assert_bounds({}, "2000-01-01")
    end
  end

  it "returns null bounds for an empty feed instead of inventing an identity rate" do
    assert_bounds({}, nil)
    assert_bounds({ base: "USD", quotes: "USD" }, nil)
    assert_bounds({ providers: "MISSING" }, nil)
  end

  it "identifies the selected feed and normalizes the filters" do
    _(coverage.slice("base", "quotes",
                     "providers",)).must_equal({ "base" => "EUR", "quotes" => nil, "providers" => nil })
    _(coverage(base: "usd", providers: "bis", quotes: "zar").slice("base", "quotes", "providers"))
      .must_equal({ "base" => "USD", "quotes" => ["ZAR"], "providers" => ["BIS"] })
  end

  it "rejects unsupported filters and invalid currencies rather than silently changing their meaning" do
    [{ date: "1900-01-31" }, { group: "month" }, { scope: "all" }, { base: "NOTREAL" }].each do |params|
      get "/coverage", params

      _(last_response.status).must_equal(422)
    end
  end
end
