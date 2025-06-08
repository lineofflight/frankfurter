# frozen_string_literal: true

require_relative "helper"
require "rack/test"
require "app"

describe App do
  include Rack::Test::Methods

  let(:app) { App.freeze }
  let(:headers) { last_response.headers }

  it "serves static files" do
    ["/", "/favicon.ico", "/robots.txt", "/v1/openapi.json"].each do |path|
      get path

      _(last_response).must_be(:ok?)
      _(headers["Cache-Control"]).must_equal("public, max-age=900")
    end
  end

  it "returns JSON for 404" do
    get "/nonexistent"

    _(last_response.status).must_equal(404)
    _(last_response.headers["Content-Type"]).must_equal("application/json")
    json = Oj.load(last_response.body)

    _(json["message"]).must_equal("not found")
  end

  it "routes /v1 to V1 handler" do
    get "/v1/latest"

    _(last_response).must_be(:ok?)
  end

  it "allows cross-origin requests" do
    ["/v1/", "/v1/latest", "/v1/2012-11-20"].each do |path|
      header "Origin", "*"
      get path

      assert headers.key?("Access-Control-Allow-Methods")
    end
  end

  it "responds to preflight requests" do
    ["/v1/", "/v1/latest", "/v1/2012-11-20"].each do |path|
      header "Origin", "*"
      header "Access-Control-Request-Method", "GET"
      header "Access-Control-Request-Headers", "Content-Type"
      options path

      assert headers.key?("Access-Control-Allow-Methods")
    end
  end
end
