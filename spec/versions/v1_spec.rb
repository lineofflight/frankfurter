# frozen_string_literal: true

require_relative "../helper"
require "rack/test"
require "versions/v1"

describe Versions::V1 do
  include Rack::Test::Methods

  let(:app) { Versions::V1.freeze }
  let(:json) { Oj.load(last_response.body) }
  let(:headers) { last_response.headers }
  let(:historical_date) { Fixtures.business_day(30).to_s }
  let(:range_start) { (Fixtures.latest_date - 365).to_s }
  let(:range_end) { Fixtures.latest_date.to_s }

  it "returns latest quotes" do
    get "/latest"

    _(last_response).must_be(:ok?)
  end

  it "sets base currency" do
    get "/latest"
    res = Oj.load(last_response.body)
    get "/latest?from=USD"

    _(json).wont_equal(res)
  end

  it "sets base amount" do
    get "/latest?amount=10"

    _(json["rates"]["USD"]).must_be(:>, 10)
  end

  it "filters symbols" do
    get "/latest?to=USD"

    _(json["rates"].keys).must_equal(["USD"])
  end

  it "returns historical quotes" do
    get "/#{historical_date}"

    _(json["rates"]).wont_be(:empty?)
    _(json["date"]).must_equal(historical_date)
  end

  it "works around holidays" do
    sunday = Fixtures.recent_sunday.to_s
    get "/#{sunday}"

    _(json["rates"]).wont_be(:empty?)
  end

  it "returns latest quotes when querying future date" do
    tomorrow = (Date.today + 1).to_s
    get "/#{tomorrow}"

    _(last_response).must_be(:ok?)
  end

  it "returns an ETag" do
    ["/latest", "/#{historical_date}"].each do |path|
      get path

      _(headers["ETag"]).wont_be_nil
    end
  end

  it "returns a cache control header" do
    ["/latest", "/#{historical_date}"].each do |path|
      get path

      _(headers["Cache-Control"]).wont_be_nil
    end
  end

  it "converts an amount" do
    get "/latest?from=GBP&to=USD&amount=100"

    _(json["rates"]["USD"]).must_be(:>, 100)
  end

  it "returns rates for a given period" do
    get "/#{range_start}..#{range_end}"

    _(json["start_date"]).wont_be(:empty?)
    _(json["end_date"]).wont_be(:empty?)
    _(json["rates"]).wont_be(:empty?)
  end

  it "returns rates when given period does not include end date" do
    get "/#{range_start}.."

    _(json["start_date"]).wont_be(:empty?)
    _(json["end_date"]).wont_be(:empty?)
    _(json["rates"]).wont_be(:empty?)
  end

  it "returns currencies" do
    get "/currencies"

    _(json["USD"]).must_equal("United States Dollar")
  end

  it "returns empty currencies when no data" do
    Rate.dataset.delete
    get "/currencies"

    _(last_response).must_be(:ok?)
    _(json).must_be(:empty?)
  end

  it "sets charset to utf-8" do
    get "/currencies"

    _(last_response.headers["content-type"]).must_be(:end_with?, "charset=utf-8")
  end
end
