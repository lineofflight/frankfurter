# frozen_string_literal: true

require_relative "../../helper"
require "rack/test"
require "versions/v2"

describe "Provider currency routes" do
  include Rack::Test::Methods

  let(:app) { Versions::V2.freeze }
  let(:date) { Fixtures.latest_date }

  before do
    Rate.dataset.insert(provider: "ECB", date:, base: "EUR", quote: "ZZZ", mid: 2.345678)
    Provider["ECB"].send(:refresh_rollups, [date])
  end

  it "serves an unknown stored quote through either provider route" do
    ["/providers/ECB/rate/EUR/ZZZ", "/rate/EUR/ZZZ?providers=ECB"].each do |path|
      get path

      _(last_response.status).must_equal(200)
      _(Oj.load(last_response.body)["rate"]).must_equal(2.345678)
    end
  end

  it "derives a rate using an unknown stored base without currency metadata" do
    get "/providers/ECB/rate/ZZZ/EUR"

    _(last_response.status).must_equal(200)
    _(Oj.load(last_response.body)["rate"]).must_equal(0.42632)
  end

  it "serves rounded unknown quotes from provider rollups" do
    get "/providers/ECB/rates?base=EUR&quotes=ZZZ&from=#{date - 7}&to=#{date}&group=month"

    _(last_response.status).must_equal(200)
    _(Oj.load(last_response.body).first["rate"]).must_equal(2.3457)
  end

  it "rejects unknown codes outside the selected provider's stored rows" do
    ["/rate/EUR/ZZZ", "/providers/BOC/rate/EUR/ZZZ", "/providers/ECB/rate/EUR/QQQ"].each do |path|
      get path

      _(last_response.status).must_equal(422)
    end
  end
end
