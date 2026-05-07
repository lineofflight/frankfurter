# frozen_string_literal: true

require_relative "helper"
require "rack/test"
require "app"

describe App do
  include Rack::Test::Methods

  let(:app) { App.freeze }
  let(:headers) { last_response.headers }

  it "serves root" do
    get "/"

    _(last_response).must_be(:ok?)
    _(headers["Cache-Control"]).must_equal("public, max-age=86400")
    json = Oj.load(last_response.body)

    _(json["name"]).must_equal("Frankfurter")
    _(json["versions"]["v1"]["openapi"]).must_equal("/v1/openapi.json")
    _(json["versions"]["v1"]["status"]).must_equal("frozen")
    _(json["versions"]["v2"]["openapi"]).must_equal("/v2/openapi.json")
    _(json["versions"]["v2"]["status"]).must_equal("current")
  end

  it "serves v1 root" do
    get "/v1"

    _(last_response).must_be(:ok?)
    json = Oj.load(last_response.body)

    _(json["version"]).must_equal("v1")
    _(json["status"]).must_equal("frozen")
    _(json["openapi"]).must_equal("/v1/openapi.json")
  end

  it "serves v2 root" do
    get "/v2"

    _(last_response).must_be(:ok?)
    json = Oj.load(last_response.body)

    _(json["version"]).must_equal("v2")
    _(json["status"]).must_equal("current")
    _(json["openapi"]).must_equal("/v2/openapi.json")
  end

  it "serves static files" do
    ["/favicon.ico", "/robots.txt", "/v1/openapi.json"].each do |path|
      get path

      _(last_response).must_be(:ok?)
      _(headers["Cache-Control"]).must_equal("public, max-age=86400")
    end
  end

  it "returns JSON for 404" do
    get "/nonexistent"

    _(last_response.status).must_equal(404)
    _(last_response.headers["Content-Type"]).must_equal("application/json")
    json = Oj.load(last_response.body)

    _(json["message"]).must_equal("not found")
  end

  describe "error responses are not cached" do
    [
      ["root 404", "/nonexistent", 404],
      ["v1 404", "/v1/1000-01-01", 404],
      ["v2 422", "/v2/rates?date=not-a-date", 422],
      ["v2 404", "/v2/currency/xyz", 404],
      ["v2 406", "/v2/currencies.csv", 406],
    ].each do |label, path, status|
      it "sets Cache-Control: no-store on #{label}" do
        get path

        _(last_response.status).must_equal(status)
        _(last_response.headers["cache-control"]).must_equal("no-store")
      end
    end
  end

  it "routes /v1 to V1 handler" do
    get "/v1/latest"

    _(last_response).must_be(:ok?)
  end

  it "allows cross-origin requests" do
    ["/v1/", "/v1/latest", "/v1/#{Fixtures.latest_date - 30}"].each do |path|
      header "Origin", "*"
      get path

      assert headers.key?("Access-Control-Allow-Methods")
    end
  end

  it "responds to preflight requests" do
    ["/v1/", "/v1/latest", "/v1/#{Fixtures.latest_date - 30}"].each do |path|
      header "Origin", "*"
      header "Access-Control-Request-Method", "GET"
      header "Access-Control-Request-Headers", "Content-Type"
      options path

      assert headers.key?("Access-Control-Allow-Methods")
    end
  end
end
