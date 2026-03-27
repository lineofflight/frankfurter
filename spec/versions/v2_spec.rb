# frozen_string_literal: true

require_relative "../helper"
require "csv"
require "rack/test"

# Skooma requires minitest/unit which was removed in Minitest 6
$LOADED_FEATURES << "minitest/unit.rb"
require "skooma"

require "versions/v2"

describe Versions::V2 do
  include Rack::Test::Methods
  include Skooma::Minitest[
    File.expand_path("../../lib/public/v2/openapi.json", __dir__),
  ]

  let(:app) { Versions::V2.freeze }
  let(:json) { Oj.load(last_response.body) }
  let(:historical_date) { Fixtures.business_day(30).to_s }
  let(:range_start) { Fixtures.business_day(60).to_s }
  let(:range_end) { Fixtures.business_day(30).to_s }
  let(:year_start) { (Fixtures.latest_date - 365).to_s }
  let(:year_end) { Fixtures.latest_date.to_s }

  it "returns latest rates" do
    get "/rates"

    _(last_response).must_be(:ok?)
    assert_conform_schema(200)
    _(json.first["base"]).must_equal("EUR")
  end

  it "returns rates for a specific date" do
    get "/rates?date=#{historical_date}"

    _(last_response).must_be(:ok?)
    assert_conform_schema(200)
    _(json.first["date"]).must_equal(historical_date)
  end

  it "snaps to nearest business day for weekends" do
    sunday = Fixtures.recent_sunday
    friday = Fixtures.preceding_friday(sunday)
    get "/rates?date=#{sunday}"

    _(last_response).must_be(:ok?)
    _(json.first["date"]).must_equal(friday.to_s)
  end

  it "returns rates for a date range" do
    get "/rates?from=#{range_start}&to=#{range_end}"

    _(last_response).must_be(:ok?)
    assert_conform_schema(200)
    dates = json.map { |r| r["date"] }.uniq

    _(dates.length).must_be(:>, 1)
  end

  it "rebases to a different currency" do
    get "/rates?base=USD"

    _(last_response).must_be(:ok?)
    assert_conform_schema(200)
    _(json.first["base"]).must_equal("USD")

    eur = json.find { |r| r["quote"] == "EUR" }

    _(eur["rate"]).must_be_kind_of(Float)
  end

  it "does not produce duplicate rows when blending providers" do
    get "/rates?base=CAD"

    _(last_response).must_be(:ok?)
    pairs = json.map { |r| [r["date"], r["quote"]] }

    _(pairs).must_equal(pairs.uniq)
  end

  it "filters quotes" do
    get "/rates?quotes=USD,GBP"

    _(last_response).must_be(:ok?)
    assert_conform_schema(200)
    quotes = json.map { |r| r["quote"] }.uniq.sort

    _(quotes).must_equal(["GBP", "USD"])
  end

  it "filters by provider" do
    get "/rates?providers=ecb"

    _(last_response).must_be(:ok?)
    assert_conform_schema(200)
  end

  it "filters by multiple providers" do
    get "/rates?providers=ecb,boc"

    _(last_response).must_be(:ok?)
    assert_conform_schema(200)
  end

  it "downsamples by week" do
    get "/rates?from=#{year_start}&to=#{year_end}&group=week"

    _(last_response).must_be(:ok?)
    dates = json.map { |r| r["date"] }.uniq

    _(dates.length).must_be(:<=, 55)
  end

  it "downsamples by month" do
    get "/rates?from=#{year_start}&to=#{year_end}&group=month"

    _(last_response).must_be(:ok?)
    dates = json.map { |r| r["date"] }.uniq

    _(dates.length).must_be(:<=, 13)
  end

  it "returns 422 for invalid group" do
    get "/rates?from=#{range_start}&to=#{range_end}&group=day"

    _(last_response.status).must_equal(422)
  end

  it "returns 422 for conflicting params" do
    get "/rates?date=#{historical_date}&from=#{range_start}"

    _(last_response.status).must_equal(422)
  end

  it "returns 422 for invalid dates" do
    get "/rates?date=not-a-date"

    _(last_response.status).must_equal(422)
  end

  it "returns an ETag for range queries" do
    get "/rates?from=#{range_start}&to=#{range_end}"

    _(last_response).must_be(:ok?)
    _(last_response.headers["ETag"]).wont_be_nil
  end

  it "returns 422 for unknown parameters" do
    get "/rates?provider=ecb"

    _(last_response.status).must_equal(422)
    _(json["message"]).must_include("unknown parameter")
  end

  it "returns rates as CSV" do
    get "/rates.csv"

    _(last_response).must_be(:ok?)
    _(last_response.content_type).must_include("text/csv")
    rows = CSV.parse(last_response.body, headers: true)

    _(rows.headers).must_equal(["date", "base", "quote", "rate"])
    _(rows.length).must_be(:>, 1)
  end

  it "returns 406 for CSV on unsupported endpoints" do
    get "/currencies.csv"

    _(last_response.status).must_equal(406)
  end

  it "returns empty array for dates before dataset" do
    get "/rates?date=1901-01-01"

    _(last_response.status).must_equal(200)
    _(Oj.load(last_response.body)).must_be_empty
  end

  it "returns currencies" do
    get "/currencies?scope=all"

    _(last_response).must_be(:ok?)
    assert_conform_schema(200)
    usd = json.find { |c| c["iso_code"] == "USD" }

    _(usd["name"]).must_equal("United States Dollar")
    _(usd["symbol"]).must_equal("$")
    _(usd["iso_numeric"]).must_equal("840")
  end

  it "returns a single currency" do
    get "/currencies/usd"

    _(last_response).must_be(:ok?)
    _(json["iso_code"]).must_equal("USD")
    _(json["name"]).must_equal("United States Dollar")
    _(json["providers"]).must_be_kind_of(Array)
    _(json["providers"]).must_include("ECB")
  end

  it "returns 404 for unknown currency" do
    get "/currencies/xyz"

    _(last_response.status).must_equal(404)
  end

  it "includes base currencies in currencies list" do
    get "/currencies?scope=all"
    eur = json.find { |c| c["iso_code"] == "EUR" }

    _(eur).wont_be_nil
  end

  it "returns a single rate pair" do
    get "/rate/EUR/USD"

    _(last_response).must_be(:ok?)
    _(json["base"]).must_equal("EUR")
    _(json["quote"]).must_equal("USD")
    _(json["rate"]).must_be_kind_of(Float)
    _(json["date"]).wont_be_nil
  end

  it "returns a single rate pair for a historical date" do
    get "/rate/EUR/USD?date=#{historical_date}"

    _(last_response).must_be(:ok?)
    _(json["date"]).must_equal(historical_date)
    _(json["base"]).must_equal("EUR")
    _(json["quote"]).must_equal("USD")
  end

  it "handles case-insensitive currency codes in rate" do
    get "/rate/eur/usd"

    _(last_response).must_be(:ok?)
    _(json["base"]).must_equal("EUR")
    _(json["quote"]).must_equal("USD")
  end

  it "filters by provider in single rate" do
    get "/rate/EUR/USD?providers=ECB"

    _(last_response).must_be(:ok?)
    _(json["base"]).must_equal("EUR")
    _(json["quote"]).must_equal("USD")
  end

  it "returns 422 for unknown currency pair" do
    get "/rate/EUR/XYZ"

    _(last_response.status).must_equal(422)
  end

  it "iterates over range results" do
    query = Versions::V2::Query.new("from" => range_start, "to" => range_end)
    records = []
    query.each { |r| records << r }

    _(records).wont_be_empty
    _(records.first).must_include(:date)
    _(records.first).must_include(:rate)
  end

  it "returns providers" do
    get "/providers"

    _(last_response).must_be(:ok?)
    assert_conform_schema(200)
    ecb = json.find { |p| p["key"] == "ECB" }

    _(ecb["name"]).must_equal("European Central Bank")
    _(ecb["start_date"]).wont_be_nil
    _(ecb["end_date"]).wont_be_nil
    _(ecb["currencies"]).must_include("USD")
  end
end
