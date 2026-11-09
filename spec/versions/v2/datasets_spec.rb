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
end
