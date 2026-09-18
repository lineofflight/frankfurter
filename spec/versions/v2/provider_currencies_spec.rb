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

  it "serves modern sucre observations only through provider routes" do
    Rate.dataset.insert(provider: "CBKKW", date:, base: "ECS", quote: "KWD", mid: 0.000012)
    Provider["CBKKW"].send(:refresh_rollups, [date])

    get "/providers/CBKKW/rate/ECS/KWD"

    _(last_response.status).must_equal(200)
    _(Oj.load(last_response.body)["rate"]).must_equal(0.000012)
    _([Rate, WeeklyRate, MonthlyRate].sum { |model| model.blendable.where(base: "ECS").count }).must_equal(0)

    get "/rate/ECS/KWD"

    _(last_response.status).must_equal(404)
  end

  it "rejects unknown codes outside the selected provider's stored rows" do
    ["/rate/EUR/ZZZ", "/providers/BOC/rate/EUR/ZZZ", "/providers/ECB/rate/EUR/QQQ"].each do |path|
      get path

      _(last_response.status).must_equal(422)
    end
  end
  it "reports unknown codes on provider metadata without adding them to the catalogue" do
    Provider["ECB"].send(:refresh_currency_summaries, ["EUR", "ZZZ"])
    get "/providers/ECB"

    _(last_response.status).must_equal(200)
    _(Oj.load(last_response.body)["unknown_currencies"]).must_equal(["ZZZ"])

    get "/currency/ZZZ"

    _(last_response.status).must_equal(404)
  end

  it "keeps a provider visible when all its currency codes are unknown" do
    Rate.dataset.where(provider: "ECB").delete
    CurrencyCoverage.where(provider_key: "ECB").delete
    Rate.dataset.insert(provider: "ECB", date:, base: "ZZZ", quote: "QQQ", mid: 2.0)
    Provider["ECB"].send(:refresh_currency_summaries, ["ZZZ", "QQQ"])
    get "/providers/ECB"

    _(last_response.status).must_equal(200)
    entry = Oj.load(last_response.body)

    _(entry["unknown_currencies"]).must_equal(["QQQ", "ZZZ"])
    _(entry["currencies"]).must_equal([])
    _(entry["start_date"]).must_equal(date.to_s)
    _(entry["end_date"]).must_equal(date.to_s)
    _(entry["publishes_missed"]).must_equal(0)

    get "/providers"

    _(Oj.load(last_response.body).find { |p| p["key"] == "ECB" }).must_equal(entry)
  end
end
