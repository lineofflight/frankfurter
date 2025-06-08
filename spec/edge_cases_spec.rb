# frozen_string_literal: true

require_relative "helper"
require "rack/test"
require "app"

describe "the app" do
  include Rack::Test::Methods

  let(:app) { App.freeze }

  def json
    Oj.load(last_response.body)
  end

  it "handles unfound pages" do
    get "/foo"

    _(last_response.status).must_equal(404)
  end

  it "will not process an invalid date" do
    get "/v1/2010-31-01"

    _(last_response).must_be(:unprocessable?)
  end

  it "will not process an invalid amount" do
    get "/v1/latest?amount=0&from=USD&to=EUR"

    _(last_response).must_be(:unprocessable?)
  end

  it "will not process a date before 2000" do
    get "/v1/1999-01-01"

    _(last_response).must_be(:not_found?)
  end

  it "will not process an unavailable base" do
    get "/v1/latest?base=UAH"

    _(last_response).must_be(:not_found?)
  end

  it "handles malformed queries" do
    get "/v1/latest?base=USD?callback=?"

    _(last_response).must_be(:not_found?)
  end

  it "does not return stale dates" do
    Currency.db.transaction do
      get "/v1/latest"
      date = json["date"]
      Currency.where(date: Currency.nearest_date_with_rates(Date.today)).delete
      get "/v1/latest"

      _(json["date"]).wont_equal(date)
      raise Sequel::Rollback
    end
  end

  it "will not process circular conversions" do
    get "/v1/latest?from=EUR&to=EUR"

    _(last_response).must_be(:unprocessable?)
    get "/v1/latest?from=USD&to=USD"

    _(last_response).must_be(:unprocessable?)
  end
end
