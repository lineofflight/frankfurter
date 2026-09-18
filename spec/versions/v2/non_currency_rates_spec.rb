# frozen_string_literal: true

require_relative "../../helper"
require "rack/test"
require "versions/v2"
require "provider/adapters/rba"

describe "Non-currency provider observations" do
  include Rack::Test::Methods

  let(:app) { Versions::V2.freeze }
  let(:date) { Fixtures.latest_date }

  before do
    [Rate, WeeklyRate, MonthlyRate].each { |model| model.dataset.delete }
    Rate.dataset.multi_insert([
      { provider: "NB", date: date - 3, base: "USD", quote: "NOK", mid: 10.0 },
      { provider: "NB", date: date - 3, base: "EUR", quote: "NOK", mid: 11.0 },
      { provider: "ECB", date: date - 7, base: "EUR", quote: "USD", mid: 1.2 },
    ])
    Provider["NB"].send(:refresh_rollups, [date - 3])
    Provider["ECB"].send(:refresh_rollups, [date - 7])
  end

  def add_indices
    Rate.dataset.multi_insert([
      { provider: "NB", date:, base: "I44", quote: "NOK", mid: 110.6 },
      { provider: "NB", date:, base: "TWI", quote: "NOK", mid: 115.432 },
    ])
    Provider["NB"].send(:refresh_rollups, [date])
    Provider["NB"].send(:refresh_currency_summaries, ["I44", "TWI", "NOK"])
  end

  it "keeps currency snapshots unchanged when a newer index arrives" do
    queries = [
      { providers: "NB,ECB", base: "USD", quotes: "EUR", date: date.to_s, expand: "providers" },
      { providers: "NB,ECB", base: "USD", quotes: "EUR" },
    ]
    before = queries.map { |params| Versions::V2::RateQuery.new(params).to_a }

    _(before.first.first[:rate]).must_equal(0.88049)
    add_indices

    queries.each_with_index do |params, i|
      _(Versions::V2::RateQuery.new(params).to_a).must_equal(before[i])
    end
  end

  [nil, "week", "month"].each do |group|
    it "keeps indices out of #{group || "daily"} multi-provider ranges" do
      params = { providers: "NB,ECB", base: "USD", from: (date - 7).to_s, to: date.to_s, group: }.compact
      before = Versions::V2::RateQuery.new(params).to_a

      _(before).wont_be_empty
      add_indices

      _(Versions::V2::RateQuery.new(params).to_a).must_equal(before)
    end
  end

  it "keeps a range-start snapshot unchanged after an index-only publication" do
    params = { providers: "NB,ECB", base: "USD", quotes: "EUR", from: date.to_s, to: date.to_s }
    before = Versions::V2::RateQuery.new(params).to_a
    add_indices

    _(Versions::V2::RateQuery.new(params).to_a).must_equal(before)
  end

  it "retains native index observations and single-provider conversions" do
    add_indices
    get "/providers/NB/rate/I44/NOK", date: date.to_s

    _(last_response.status).must_equal(200)
    _(Oj.load(last_response.body)["rate"]).must_equal(110.6)

    get "/providers/NB/rate/NOK/I44", date: date.to_s

    _(last_response.status).must_equal(200)
    _(Oj.load(last_response.body)["rate"]).must_equal(0.00904)

    [Rate, WeeklyRate, MonthlyRate].each do |model|
      _(model.where(provider: "NB", base: ["I44", "TWI"]).count).must_equal(2)
      _(model.blendable.where(base: ["I44", "TWI"]).count).must_equal(0)
    end

    get "/currencies"

    _(Oj.load(last_response.body).map { |row| row["iso_code"] } & ["I44", "TWI"]).must_be_empty
  end

  it "rejects an index base or quote when selecting several providers" do
    add_indices
    ["/rate/I44/NOK", "/rate/NOK/I44"].each do |path|
      get path, providers: "NB,ECB", date: date.to_s

      _(last_response.status).must_equal(422)
    end

    get "/rate/I44/NOK", providers: "NB,NB", date: date.to_s

    _(last_response.status).must_equal(200)
  end

  it "acknowledges reviewed indices but still reports new labels and other providers" do
    add_indices
    Rate.dataset.multi_insert([
      { provider: "NB", date:, base: "ZZZ", quote: "NOK", mid: 100 },
      { provider: "ECB", date:, base: "EUR", quote: "I44", mid: 100 },
    ])
    Provider["NB"].send(:refresh_currency_summaries, ["ZZZ"])
    Provider["ECB"].send(:refresh_currency_summaries, ["I44"])

    get "/providers/NB"

    _(last_response.status).must_equal(200)
    entry = Oj.load(last_response.body)

    _(entry["unknown_currencies"]).must_equal(["ZZZ"])
    _(entry["currencies"] & ["I44", "TWI"]).must_be_empty
    _(Provider["ECB"].unknown_currencies).must_include("I44")
  end

  it "ingests RBA's index without changing currency conversions or stored blends" do
    provider = Provider["RBA"]
    Rate.dataset.insert(provider: "RBA", date: date - 3, base: "AUD", quote: "USD", mid: 0.7)
    provider.send(:refresh_rollups, [date - 3])
    models = [BlendedRate, BlendedWeeklyRate, BlendedMonthlyRate]
    models.each(&:rebuild)
    before = models.map { |model| model.dataset.order(*model.primary_key).naked.all }
    queries = [nil, "RBA", "RBA,NB,ECB"].map do |providers|
      { providers:, base: "USD", quotes: "AUD", date: date.to_s }.compact
    end
    rates = queries.map { |params| Versions::V2::RateQuery.new(params).to_a }

    fetch = lambda do |**_, &block|
      block.call([{ date:, base: "AUD", quote: "FXRTWI", rate: 61.4 }])
    end
    provider.adapter.stub(:fetch_each, fetch) do
      Cache.stub(:purge_debounced, nil) { provider.backfill(after: date - 1) }
    end

    models.each_with_index do |model, i|
      _(model.dataset.order(*model.primary_key).naked.all).must_equal(before[i])
    end
    queries.each_with_index do |params, i|
      _(Versions::V2::RateQuery.new(params).to_a).must_equal(rates[i])
    end

    get "/providers/RBA/rate/AUD/FXRTWI", date: date.to_s

    _(last_response.status).must_equal(200)
    _(Oj.load(last_response.body)["rate"]).must_equal(61.4)

    get "/rate/AUD/FXRTWI", providers: "NB,RBA", date: date.to_s

    _(last_response.status).must_equal(422)
    _(Provider["RBA"].unknown_currencies).must_be_empty
    _(Currency.find("FXRTWI")).must_be_nil
    [Rate, WeeklyRate, MonthlyRate].each do |model|
      _(model.where(provider: "RBA", quote: "FXRTWI").count).must_equal(1)
    end
  end
end
