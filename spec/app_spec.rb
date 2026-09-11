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
    _(json["status"]).must_equal("deprecated")
    _(json["openapi"]).must_equal("/v1/openapi.json")
    _(json["docs"]).must_equal("https://frankfurter.dev/v1/")
  end

  describe "v1 deprecation headers" do
    [
      ["/v1", "/v2", 200],
      ["/v1/", "/v2", 200],
      ["/v1/currencies", "/v2/currencies", 200],
      ["/v1/latest", "/v2/rates", 200],
      ["/v1/current", "/v2/rates", 200],
      ["/v1/latest?from=USD&to=GBP&amount=2", "/v2/rates", 200],
      ["/v1/#{Fixtures.business_day(30)}", "/v2/rates", 200],
      ["/v1/#{Fixtures.business_day(30)}..#{Fixtures.latest_date}", "/v2/rates", 200],
      ["/v1/#{Fixtures.business_day(30)}..", "/v2/rates", 200],
      ["/v1/openapi.json", "/v2/openapi.json", 200],
      ["/v1/nonexistent", "/v2/rates", 404],
      ["/v1/1000-01-01", "/v2/rates", 404],
      ["/v1/latest?amount=invalid", "/v2/rates", 422],
    ].each do |path, successor, status|
      it "links #{path} to its successor" do
        get path

        _(last_response.status).must_equal(status)
        _(headers["Deprecation"]).must_equal("@1779103800")
        _(headers["Link"]).must_equal(%(<https://api.frankfurter.dev#{successor}>; rel="successor-version"))
      end
    end

    it "includes headers on HEAD responses" do
      [
        ["/v1", "/v2", 200],
        ["/v1/", "/v2", 404],
        ["/v1/currencies", "/v2/currencies", 200],
        ["/v1/latest", "/v2/rates", 200],
        ["/v1/openapi.json", "/v2/openapi.json", 200],
      ].each do |path, successor, status|
        head path

        _(last_response.status).must_equal(status)
        _(last_response.headers["Deprecation"]).must_equal("@1779103800")
        _(last_response["Link"]).must_equal(%(<https://api.frankfurter.dev#{successor}>; rel="successor-version"))
      end
    end

    it "includes headers on conditional responses" do
      [
        ["/v1/currencies", "/v2/currencies"],
        ["/v1/latest", "/v2/rates"],
        ["/v1/current", "/v2/rates"],
        ["/v1/#{Fixtures.business_day(30)}", "/v2/rates"],
        ["/v1/#{Fixtures.business_day(30)}..", "/v2/rates"],
      ].each do |path, successor|
        get path
        get path, {}, "HTTP_IF_NONE_MATCH" => last_response.headers["ETag"]

        _(last_response.status).must_equal(304)
        _(last_response.body).must_be_empty
        _(last_response.headers["Deprecation"]).must_equal("@1779103800")
        _(last_response["Link"]).must_equal(%(<https://api.frankfurter.dev#{successor}>; rel="successor-version"))
      end
    end

    it "includes headers on CORS preflight responses" do
      [
        ["/v1", "/v2"],
        ["/v1/currencies", "/v2/currencies"],
        ["/v1/latest", "/v2/rates"],
        ["/v1/openapi.json", "/v2/openapi.json"],
      ].each do |path, successor|
        options path, {}, "HTTP_ORIGIN" => "https://example.com", "HTTP_ACCESS_CONTROL_REQUEST_METHOD" => "GET"

        _(last_response).must_be(:ok?)
        _(last_response.headers["Access-Control-Allow-Origin"]).must_equal("*")
        _(last_response.headers["Deprecation"]).must_equal("@1779103800")
        _(last_response["Link"]).must_equal(%(<https://api.frankfurter.dev#{successor}>; rel="successor-version"))
      end
    end

    it "does not deprecate other routes" do
      ["/", "/v2", "/v2/currencies", "/v2/rates", "/v2/openapi.json", "/v10/latest", "/nonexistent"].each do |path|
        get path

        _(last_response.headers["Deprecation"]).must_be_nil
        _(last_response.headers["Link"]).must_be_nil
      end
    end
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

  describe "search engine indexing" do
    ["/robots.txt", "/v1/latest", "/v2/rates", "/v2/currencies", "/nonexistent"].each do |path|
      it "sets X-Robots-Tag: noindex on #{path}" do
        get path

        _(headers["x-robots-tag"]).must_equal("noindex")
      end
    end

    ["/", "/v1", "/v2", "/v1/openapi.json", "/v2/openapi.json"].each do |path|
      it "leaves #{path} indexable" do
        get path

        _(last_response).must_be(:ok?)
        _(headers["x-robots-tag"]).must_be_nil
      end
    end
  end

  it "returns JSON for 404" do
    get "/nonexistent"

    _(last_response.status).must_equal(404)
    _(last_response.headers["Content-Type"]).must_equal("application/json")
    json = Oj.load(last_response.body)

    _(json["message"]).must_equal("not found")
  end

  # Through the full middleware stack: the RequestTimeout middleware must not swallow a 503 the query generated after
  # its own (later-starting) deadline expired.
  it "delivers a v2 deadline 503 through the middleware stack" do
    slow_query = Object.new
    def slow_query.range? = true
    def slow_query.date_relative? = false
    def slow_query.cache_key = "x"

    def slow_query.each
      return to_enum(:each) unless block_given?

      raise RequestTimeout::Error, "request exceeded 90s timeout"
    end

    Versions::V2::RateQuery.stub(:new, slow_query) do
      get "/v2/rates?from=2024-01-01&to=2024-02-01"
    end

    _(last_response.status).must_equal(503)
    _(last_response.headers["cache-control"]).must_equal("no-store")
    _(Oj.load(last_response.body)["message"]).must_include("timeout")
  end

  it "does not cache a 503 from the heavy compute cap" do
    slots = HeavySlots.new(1)
    slots.try_acquire
    Versions::V2::RateQuery.stub(:heavy_slots, slots) do
      get "/v2/rates?providers=ecb&from=#{Fixtures.business_day(60)}&to=#{Fixtures.latest_date}"
    end

    _(last_response.status).must_equal(503)
    _(last_response.headers["cache-control"]).must_equal("no-store")
    _(last_response.headers["retry-after"]).must_equal("30")
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
