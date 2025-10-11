# frozen_string_literal: true

require_relative "../helper"
require "rack/test"
require "versions/v2"

describe Versions::V2 do
  include Rack::Test::Methods

  let(:app) { Versions::V2.freeze }
  let(:json) { Oj.load(last_response.body) }
  let(:headers) { last_response.headers }

  describe "GET /latest" do
    it "returns latest quotes with source field" do
      get "/latest?from=EUR&to=USD"

      _(last_response).must_be(:ok?)
      _(json["source"]).must_equal("ECB")
      _(json["base"]).must_equal("EUR")
      _(json["rates"]).must_include("USD")
    end

    it "returns rates with default base when from parameter omitted" do
      get "/latest"

      _(last_response).must_be(:ok?)
      _(json["base"]).must_equal("EUR")
    end

    it "filters target currencies with to parameter" do
      get "/latest?from=EUR&to=USD,GBP"

      _(json["rates"].keys.sort).must_equal(["GBP", "USD"])
    end

    it "converts amounts" do
      get "/latest?from=EUR&to=USD&amount=100"

      _(json["amount"]).must_equal(100)
      _(json["rates"]["USD"]).must_be(:>, 100)
    end

    it "returns error when from currency has no native source" do
      get "/latest?from=USD&to=EUR"

      _(last_response.status).must_equal(400)
      _(json["error"]).must_match(/No source found|No data available/)
    end

    it "allows cross-source conversion with explicit source parameter" do
      get "/latest?from=USD&to=EUR&source=ECB"

      _(last_response).must_be(:ok?)
      _(json["source"]).must_equal("ECB")
      _(json["base"]).must_equal("USD")
    end

    it "returns error for invalid source" do
      get "/latest?from=EUR&to=USD&source=INVALID"

      _(last_response.status).must_equal(400)
      _(json["error"]).must_match(/Source.*not found/)
    end

    it "returns ETag header" do
      get "/latest?from=EUR"

      _(headers["ETag"]).wont_be_nil
    end

    it "returns Cache-Control header" do
      get "/latest?from=EUR"

      _(headers["Cache-Control"]).must_include("public")
      _(headers["Cache-Control"]).must_include("max-age")
    end

    it "sets charset to utf-8" do
      get "/latest?from=EUR"

      _(last_response.headers["content-type"]).must_be(:end_with?, "charset=utf-8")
    end
  end

  describe "GET /:date" do
    it "returns historical quotes for specific date" do
      get "/2012-11-20?from=EUR&to=USD"

      _(last_response).must_be(:ok?)
      _(json["date"]).must_equal("2012-11-20")
      _(json["source"]).must_equal("ECB")
      _(json["rates"]).must_include("USD")
    end

    it "works around weekends and holidays" do
      get "/2010-01-01?from=EUR"

      _(last_response).must_be(:ok?)
      _(json["rates"]).wont_be(:empty?)
    end

    it "returns latest quotes when querying future date" do
      tomorrow = (Date.today + 1).to_s
      get "/#{tomorrow}?from=EUR"

      _(last_response).must_be(:ok?)
    end

    it "allows explicit source for historical data" do
      get "/2012-11-20?from=USD&to=EUR&source=ECB"

      _(last_response).must_be(:ok?)
      _(json["source"]).must_equal("ECB")
      _(json["base"]).must_equal("USD")
    end

    it "converts amounts for historical dates" do
      get "/2012-11-20?from=EUR&to=USD&amount=50"

      _(json["amount"]).must_equal(50)
      _(json["rates"]["USD"]).must_be(:>, 50)
    end

    it "returns error when from currency has no native source" do
      get "/2012-11-20?from=USD"

      _(last_response.status).must_equal(400)
    end
  end

  describe "GET /:start_date..:end_date" do
    it "returns rates for date range" do
      get "/2010-01-01..2010-01-31?from=EUR&to=USD"

      _(last_response).must_be(:ok?)
      _(json["start_date"]).wont_be_nil
      _(json["end_date"]).wont_be_nil
      _(json["source"]).must_equal("ECB")
      _(json["rates"]).wont_be(:empty?)
    end

    it "returns rates when end date is omitted" do
      get "/2010-01-01..?from=EUR"

      _(last_response).must_be(:ok?)
      _(json["start_date"]).wont_be(:empty?)
      _(json["end_date"]).wont_be(:empty?)
    end

    it "allows explicit source for date ranges" do
      get "/2010-01-01..2010-01-31?from=USD&to=EUR&source=ECB"

      _(last_response).must_be(:ok?)
      _(json["source"]).must_equal("ECB")
    end

    it "converts amounts for date ranges" do
      get "/2010-01-01..2010-01-31?from=EUR&to=USD&amount=100"

      _(json["amount"]).must_equal(100)
    end
  end

  describe "GET /sources" do
    it "returns list of available sources" do
      get "/sources"

      _(last_response).must_be(:ok?)
      _(json["sources"]).must_be_instance_of(Array)
      _(json["sources"]).wont_be(:empty?)
    end

    it "includes source details" do
      get "/sources"

      source = json["sources"].first
      _(source).must_include("code")
      _(source).must_include("name")
      _(source).must_include("base_currency")
    end

    it "includes ECB as a source" do
      get "/sources"

      ecb = json["sources"].find { |s| s["code"] == "ECB" }
      _(ecb).wont_be_nil
      _(ecb["name"]).must_equal("European Central Bank")
      _(ecb["base_currency"]).must_equal("EUR")
    end
  end
end
