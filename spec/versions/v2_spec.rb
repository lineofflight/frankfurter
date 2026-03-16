# frozen_string_literal: true

require_relative "../helper"
require "rack/test"
require "versions/v2"

describe Versions::V2 do
  include Rack::Test::Methods

  let(:app) { Versions::V2.freeze }
  let(:json) { Oj.load(last_response.body) }

  it "returns latest rates" do
    get "/rates"

    _(last_response).must_be(:ok?)
    _(json["base"]).must_equal("EUR")
    _(json["rates"]).must_be_kind_of(Array)
    _(json["rates"].length).must_equal(1)
    _(json["rates"].first["USD"]).must_be_kind_of(Float)
  end

  it "returns rates for a specific date" do
    get "/rates?date=2024-01-15"

    _(last_response).must_be(:ok?)
    _(json["rates"].first["date"]).must_equal("2024-01-15")
  end

  it "returns rates for a date range" do
    get "/rates?from=2024-01-01&to=2024-01-31"

    _(last_response).must_be(:ok?)
    _(json["rates"].length).must_be(:>, 1)
  end

  it "rebases to a different currency" do
    get "/rates?base=USD"

    _(last_response).must_be(:ok?)
    _(json["base"]).must_equal("USD")
    _(json["rates"].first.keys).wont_include("USD")
    _(json["rates"].first["EUR"]).must_be_kind_of(Float)
  end

  it "filters symbols" do
    get "/rates?symbols=USD,GBP"

    _(last_response).must_be(:ok?)
    rates = json["rates"].first

    _(rates.keys.sort).must_equal(["GBP", "USD", "date"])
  end

  it "filters by provider" do
    get "/rates?provider=ecb"

    _(last_response).must_be(:ok?)
    _(json["rates"]).wont_be(:empty?)
  end

  it "returns 400 for conflicting params" do
    get "/rates?date=2024-01-15&from=2024-01-01"

    _(last_response.status).must_equal(400)
  end

  it "returns 404 for dates before dataset" do
    get "/rates?date=1901-01-01"

    _(last_response.status).must_equal(404)
  end

  it "returns consistent response shape" do
    get "/rates"
    latest = json

    get "/rates?date=2024-01-15"
    historical = json

    _(latest.keys.sort).must_equal(historical.keys.sort)
    _(latest["rates"].first.keys).must_include("date")
  end

  it "returns currencies" do
    get "/currencies"

    _(last_response).must_be(:ok?)
    _(json["USD"]).must_be_kind_of(Hash)
    _(json["USD"]["name"]).must_equal("United States Dollar")
    _(json["USD"]["providers"]).must_include("ECB")
  end

  it "includes base currencies in currencies list" do
    get "/currencies"

    _(json["EUR"]).must_be_kind_of(Hash)
  end

  it "returns providers" do
    get "/providers"

    _(last_response).must_be(:ok?)
    _(json["ECB"]).must_be_kind_of(Hash)
    _(json["ECB"]["name"]).must_equal("European Central Bank")
    _(json["ECB"]["base"]).must_equal("EUR")
  end
end
