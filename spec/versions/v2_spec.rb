# frozen_string_literal: true

require_relative "../helper"
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

  it "returns latest rates" do
    get "/rates"

    _(last_response).must_be(:ok?)
    assert_conform_schema(200)
    _(json.first["base"]).must_equal("EUR")
  end

  it "returns rates for a specific date" do
    get "/rates?date=2024-01-15"

    _(last_response).must_be(:ok?)
    assert_conform_schema(200)
    _(json.first["date"]).must_equal("2024-01-15")
  end

  it "snaps to nearest business day for weekends" do
    get "/rates?date=2024-01-14" # Sunday

    _(last_response).must_be(:ok?)
    _(json.first["date"]).must_equal("2024-01-12") # Friday
  end

  it "returns rates for a date range" do
    get "/rates?from=2024-01-01&to=2024-01-31"

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
    get "/rates?providers=ecb,tcmb"

    _(last_response).must_be(:ok?)
    assert_conform_schema(200)
  end

  it "downsamples by week" do
    get "/rates?from=2024-01-01&to=2024-12-31&group=week"

    _(last_response).must_be(:ok?)
    dates = json.map { |r| r["date"] }.uniq

    _(dates.length).must_be(:<, 54)
  end

  it "downsamples by month" do
    get "/rates?from=2024-01-01&to=2024-12-31&group=month"

    _(last_response).must_be(:ok?)
    dates = json.map { |r| r["date"] }.uniq

    _(dates.length).must_be(:<=, 12)
  end

  it "returns 422 for invalid group" do
    get "/rates?from=2024-01-01&to=2024-12-31&group=day"

    _(last_response.status).must_equal(422)
  end

  it "returns 422 for conflicting params" do
    get "/rates?date=2024-01-15&from=2024-01-01"

    _(last_response.status).must_equal(422)
  end

  it "returns 422 for invalid dates" do
    get "/rates?date=not-a-date"

    _(last_response.status).must_equal(422)
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
