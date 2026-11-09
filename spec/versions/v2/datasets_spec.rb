# frozen_string_literal: true

require_relative "../../helper"
require "rack/test"

# Skooma requires minitest/unit which was removed in Minitest 6
$LOADED_FEATURES << "minitest/unit.rb"
require "skooma"

require "versions/v2"

# NBP publishes Table A daily and Table B weekly. Both are default datasets of the provider, so NBP selects them
# together and its rows report NBP, while each table keeps its own coverage, health and key.
describe "Provider datasets" do
  include Rack::Test::Methods
  include Skooma::Minitest[
    File.expand_path("../../../lib/public/v2/openapi.json", __dir__),
  ]

  let(:app) { Versions::V2.freeze }
  let(:json) { Oj.load(last_response.body) }

  def nbp(key, date, base, mid)
    { dataset_key: key, date: Date.parse(date), base:, quote: "PLN", mid: }
  end

  def store(rows)
    Rate.dataset.multi_insert(rows)
    Provider["NBP"].dup.send(:refresh_rollups, rows.map { |row| row[:date] }.uniq)
    Provider["NBP"].dup.send(:refresh_currency_summaries, rows.flat_map { |row| [row[:base], row[:quote]] }.uniq)
  end

  describe "on providers" do
    before do
      store([
        nbp("NBP.A", "2025-03-03", "USD", 3.9), nbp("NBP.A", "2025-03-04", "USD", 3.91),
        nbp("NBP.B", "2025-02-26", "AED", 1.06),
      ])
    end

    it "lists each table with its own coverage and health beside the combined fields" do
      get "/providers/NBP"

      _(last_response).must_be(:ok?)
      assert_conform_schema(200)
      _(json.keys.last).must_equal("datasets")
      _(json.slice("start_date", "end_date", "publish_cadence", "frequency", "currencies")).must_equal(
        "start_date" => "2025-02-26", "end_date" => "2025-03-04", "publish_cadence" => "daily",
        "frequency" => "daily", "currencies" => ["AED", "PLN", "USD"],
      )
      a, b = json["datasets"]

      _(a.slice("key", "default", "start_date", "end_date", "publish_cadence", "frequency", "currencies")).must_equal(
        "key" => "NBP.A", "default" => true, "start_date" => "2025-03-03", "end_date" => "2025-03-04",
        "publish_cadence" => "daily", "frequency" => "daily", "currencies" => ["PLN", "USD"],
      )
      _(b.slice("key", "default", "start_date", "end_date", "publish_cadence", "frequency", "currencies")).must_equal(
        "key" => "NBP.B", "default" => true, "start_date" => "2025-02-26", "end_date" => "2025-02-26",
        "publish_cadence" => "weekly", "frequency" => "weekly", "currencies" => ["AED", "PLN"],
      )
      _(b["publishes_missed"]).must_equal(Dataset["NBP.B"].publishes_missed(last_date: "2025-02-26"))
      _(json["publishes_missed"]).must_equal(Dataset["NBP.A"].publishes_missed(last_date: "2025-03-04"))
    end

    it "lists a provider's only series as one default dataset mirroring the provider" do
      get "/providers"

      assert_conform_schema(200)
      ecb = json.find { |provider| provider["key"] == "ECB" }
      dataset = ecb["datasets"].first
      shared = ["rate_type", "pivot_currency", "data_url", "start_date", "end_date", "publish_cadence", "frequency",
                "publishes_missed", "currencies", "unknown_currencies",]

      _(ecb["datasets"].size).must_equal(1)
      _(dataset["key"]).must_equal("ECB")
      _(dataset["default"]).must_equal(true)
      _(dataset.slice(*shared)).must_equal(ecb.slice(*shared))
    end
  end

  # Table B quotes neither the euro nor the dollar, so on its own it can't reach the default base.
  describe "with a base the selection never quotes" do
    before do
      store([nbp("NBP.A", "2025-02-26", "USD", 3.91), nbp("NBP.B", "2025-02-26", "AED", 1.06)])
    end

    it "refuses the base and names the one to use" do
      get "/rates", providers: "NBP.B"

      _(last_response.status).must_equal(422)
      _(json["message"]).must_equal("NBP.B doesn't quote EUR; try base=PLN")
    end

    it "refuses it on the single-pair route too" do
      get "/rate/USD/AED", providers: "NBP.B"

      _(last_response.status).must_equal(422)
    end

    it "answers once one selected dataset quotes it" do
      get "/rates", providers: "NBP.B", base: "PLN", date: "2025-02-26"

      _(json.map { |row| row["quote"] }).must_equal(["AED", "PLN"])
      get "/rates", providers: "NBP", base: "USD", date: "2025-02-26"

      _(json.map { |row| row["quote"] }).must_equal(["AED", "PLN"])
    end

    it "serves a dataset key on the provider rate routes as providers= does" do
      get "/rates", providers: "NBP.B", base: "PLN", date: "2025-02-26"
      expected = last_response.body
      get "/providers/nbp.b/rates", base: "PLN", date: "2025-02-26"

      _(last_response).must_be(:ok?)
      _(last_response.body).must_equal(expected)
      get "/providers/NBP.B/rate/PLN/AED", date: "2025-02-26"

      _(json["quote"]).must_equal("AED")
      get "/providers/NBP.B/rates"

      _(last_response.status).must_equal(422)
    end

    it "keeps provider details to provider keys" do
      get "/providers/NBP.B"

      _(last_response.status).must_equal(404)
    end

    it "leaves an unknown provider selecting nothing" do
      get "/rates", providers: "NOPE"

      _(last_response).must_be(:ok?)
      _(json).must_equal([])
    end
  end

  # Table A quotes the ruble with a one-day typo on March 4, then the ruble moves to Table B. NBP's stored March rollup
  # pools both tables under NBP.B.
  describe "grouped by one table" do
    before do
      store([
        nbp("NBP.A", "2025-03-03", "RUB", 0.03), nbp("NBP.A", "2025-03-04", "RUB", 0.30),
        nbp("NBP.A", "2025-03-05", "RUB", 0.03), nbp("NBP.B", "2025-03-12", "RUB", 0.03),
      ])
      RateSpike.refresh(["NBP.A", "NBP.B"], [Date.new(2025, 3, 4)])
    end

    def monthly(providers)
      get "/rates", providers:, base: "RUB", quotes: "PLN", from: "2025-03-01", to: "2025-03-31", group: "month"

      Oj.load(last_response.body).map { |row| [row["date"], row["rate"]] }
    end

    it "averages each table's own daily rows instead of the pooled rollup" do
      _(monthly("NBP.B")).must_equal([["2025-03-01", 0.03]])
      _(monthly("NBP.A")).must_equal([["2025-03-01", 0.12]])
      _(monthly("NBP")).must_equal([["2025-03-01", 0.0975]])
    end

    it "averages weeks the same way" do
      get "/rates", providers: "NBP.A", base: "RUB", quotes: "PLN", from: "2025-03-01", to: "2025-03-16", group: "week"

      _(json.map { |row| row["rate"] }).must_equal([0.12])
    end

    # Provider queries serve what the source published, typos included: the spike flag keeps the typo out of blends
    # only, as it does for the pooled NBP rollup and every provider's own averages.
    it "keeps the flagged typo in the table's own average, as in the pooled one" do
      _(RateSpike.dataset.select_map([:dataset_key, :date]).map { |key, date| [key, date.to_s] })
        .must_equal([["NBP.A", "2025-03-04"]])
      _(monthly("NBP.A")).must_equal([["2025-03-01", 0.12]])
    end
  end
end
