# frozen_string_literal: true

require_relative "../helper"
require "csv"
require "rack/test"

# Skooma requires minitest/unit which was removed in Minitest 6
$LOADED_FEATURES << "minitest/unit.rb"
require "skooma"

require "versions/v2"

describe Versions::V2 do
  include Rack::Test::Methods
  include Skooma::Minitest[
    File.expand_path("../../lib/public/v2/openapi.json", __dir__),
  ]

  let(:app) { Versions::V2.freeze }
  let(:json) { Oj.load(last_response.body) }
  let(:historical_date) { Fixtures.business_day(30).to_s }
  let(:range_start) { Fixtures.business_day(60).to_s }
  let(:range_end) { Fixtures.business_day(30).to_s }
  let(:year_start) { (Fixtures.latest_date - 365).to_s }
  let(:year_end) { Fixtures.latest_date.to_s }

  it "returns latest rates" do
    get "/rates"

    _(last_response).must_be(:ok?)
    assert_conform_schema(200)
    _(json.first["base"]).must_equal("EUR")
  end

  it "returns rates for a specific date" do
    get "/rates?date=#{historical_date}"

    _(last_response).must_be(:ok?)
    assert_conform_schema(200)
    _(json.first["date"]).must_equal(historical_date)
  end

  it "snaps to nearest business day for weekends" do
    sunday = Fixtures.recent_sunday
    friday = Fixtures.preceding_friday(sunday)
    get "/rates?date=#{sunday}"

    _(last_response).must_be(:ok?)
    _(json.first["date"]).must_equal(friday.to_s)
  end

  it "returns rates for a date range" do
    get "/rates?from=#{range_start}&to=#{range_end}"

    _(last_response).must_be(:ok?)
    assert_conform_schema(200)
    dates = json.map { |r| r["date"] }.uniq

    _(dates.length).must_be(:>, 1)
  end

  it "rebases to a different currency" do
    get "/rates?base=USD"

    _(last_response).must_be(:ok?)
    assert_conform_schema(200)
    _(json.first["base"]).must_equal("USD")

    eur = json.find { |r| r["quote"] == "EUR" }

    _(eur["rate"]).must_be_kind_of(Float)
  end

  it "does not produce duplicate rows when blending providers" do
    get "/rates?base=CAD"

    _(last_response).must_be(:ok?)
    pairs = json.map { |r| [r["date"], r["quote"]] }

    _(pairs).must_equal(pairs.uniq)
  end

  it "filters quotes" do
    get "/rates?quotes=USD,GBP"

    _(last_response).must_be(:ok?)
    assert_conform_schema(200)
    quotes = json.map { |r| r["quote"] }.uniq.sort

    _(quotes).must_equal(["GBP", "USD"])
  end

  it "filters by provider" do
    get "/rates?providers=ecb"

    _(last_response).must_be(:ok?)
    assert_conform_schema(200)
  end

  it "filters by multiple providers" do
    get "/rates?providers=ecb,boc"

    _(last_response).must_be(:ok?)
    assert_conform_schema(200)
  end

  it "downsamples by week" do
    get "/rates?from=#{year_start}&to=#{year_end}&group=week"

    _(last_response).must_be(:ok?)
    dates = json.map { |r| r["date"] }.uniq

    _(dates.length).must_be(:<=, 55)
  end

  it "downsamples by month" do
    get "/rates?from=#{year_start}&to=#{year_end}&group=month"

    _(last_response).must_be(:ok?)
    dates = json.map { |r| r["date"] }.uniq

    _(dates.length).must_be(:<=, 13)
  end

  it "returns 422 for invalid group" do
    get "/rates?from=#{range_start}&to=#{range_end}&group=day"

    _(last_response.status).must_equal(422)
  end

  it "returns 422 for conflicting params" do
    get "/rates?date=#{historical_date}&from=#{range_start}"

    _(last_response.status).must_equal(422)
  end

  it "returns 422 for invalid dates" do
    get "/rates?date=not-a-date"

    _(last_response.status).must_equal(422)
  end

  it "returns an ETag for range queries" do
    get "/rates?from=#{range_start}&to=#{range_end}"

    _(last_response).must_be(:ok?)
    _(last_response.headers["ETag"]).wont_be_nil
  end

  it "returns 422 for unknown parameters" do
    get "/rates?provider=ecb"

    _(last_response.status).must_equal(422)
    _(json["message"]).must_include("unknown parameter")
  end

  it "returns rates as CSV" do
    get "/rates.csv"

    _(last_response).must_be(:ok?)
    _(last_response.content_type).must_include("text/csv")
    rows = CSV.parse(last_response.body, headers: true)

    _(rows.headers).must_equal(["date", "base", "quote", "rate"])
    _(rows.length).must_be(:>, 1)
  end

  it "returns 406 for CSV on unsupported endpoints" do
    get "/currencies.csv"

    _(last_response.status).must_equal(406)
  end

  it "returns empty array for dates before dataset" do
    get "/rates?date=1901-01-01"

    _(last_response.status).must_equal(200)
    _(Oj.load(last_response.body)).must_be_empty
  end

  it "returns currencies" do
    get "/currencies?scope=all"

    _(last_response).must_be(:ok?)
    assert_conform_schema(200)
    usd = json.find { |c| c["iso_code"] == "USD" }

    _(usd["name"]).must_equal("United States Dollar")
    _(usd["symbol"]).must_equal("$")
    _(usd["iso_numeric"]).must_equal("840")
  end

  it "returns a single currency" do
    get "/currency/usd"

    _(last_response).must_be(:ok?)
    _(json["iso_code"]).must_equal("USD")
    _(json["name"]).must_equal("United States Dollar")
    _(json["providers"]).must_be_kind_of(Array)
    _(json["providers"]).must_include("ECB")
  end

  it "returns 404 for unknown currency" do
    get "/currency/xyz"

    _(last_response.status).must_equal(404)
  end

  it "filters currencies by provider" do
    get "/currencies?providers=ecb"

    _(last_response).must_be(:ok?)
    codes = json.map { |c| c["iso_code"] }

    _(codes).must_include("USD")
    _(codes).must_include("EUR")
    _(codes).wont_include("BMD")
  end

  it "includes base currencies in currencies list" do
    get "/currencies?scope=all"
    eur = json.find { |c| c["iso_code"] == "EUR" }

    _(eur).wont_be_nil
  end

  it "returns a single rate pair" do
    get "/rate/EUR/USD"

    _(last_response).must_be(:ok?)
    _(json["base"]).must_equal("EUR")
    _(json["quote"]).must_equal("USD")
    _(json["rate"]).must_be_kind_of(Float)
    _(json["date"]).wont_be_nil
  end

  it "returns a single rate pair for a historical date" do
    get "/rate/EUR/USD?date=#{historical_date}"

    _(last_response).must_be(:ok?)
    _(json["date"]).must_equal(historical_date)
    _(json["base"]).must_equal("EUR")
    _(json["quote"]).must_equal("USD")
  end

  it "handles case-insensitive currency codes in rate" do
    get "/rate/eur/usd"

    _(last_response).must_be(:ok?)
    _(json["base"]).must_equal("EUR")
    _(json["quote"]).must_equal("USD")
  end

  it "filters by provider in single rate" do
    get "/rate/EUR/USD?providers=ECB"

    _(last_response).must_be(:ok?)
    _(json["base"]).must_equal("EUR")
    _(json["quote"]).must_equal("USD")
  end

  it "returns 422 for unknown currency pair" do
    get "/rate/EUR/XYZ"

    _(last_response.status).must_equal(422)
  end

  it "sets Vary header on NDJSON responses" do
    get "/rates", {}, { "HTTP_ACCEPT" => "application/x-ndjson" }

    _(last_response).must_be(:ok?)
    _(last_response.headers["Vary"]).must_equal("Accept")
  end

  it "prefers CSV extension over NDJSON accept header" do
    get "/rates.csv", {}, { "HTTP_ACCEPT" => "application/x-ndjson" }

    _(last_response).must_be(:ok?)
    _(last_response.content_type).must_include("text/csv")
  end

  it "returns NDJSON when requested" do
    get(
      "/rates?from=#{range_start}&to=#{range_end}",
      {},
      { "HTTP_ACCEPT" => "application/x-ndjson" },
    )

    _(last_response).must_be(:ok?)
    _(last_response.content_type).must_include("application/x-ndjson")
    lines = last_response.body.strip.split("\n")

    _(lines.length).must_be(:>, 1)
    parsed = Oj.load(lines.first)

    _(parsed["date"]).wont_be_nil
    _(parsed["rate"]).wont_be_nil
  end

  it "returns NDJSON for single-date queries when requested" do
    get "/rates", {}, { "HTTP_ACCEPT" => "application/x-ndjson" }

    _(last_response).must_be(:ok?)
    _(last_response.content_type).must_include("application/x-ndjson")
    lines = last_response.body.strip.split("\n")
    parsed = Oj.load(lines.first)

    _(parsed["date"]).wont_be_nil
  end

  it "streams a valid JSON array for range queries" do
    get "/rates?from=#{range_start}&to=#{range_end}"

    _(last_response).must_be(:ok?)
    _(last_response.content_type).must_include("application/json")
    parsed = Oj.load(last_response.body)

    _(parsed).must_be_kind_of(Array)
    _(parsed.first["date"]).wont_be_nil
  end

  it "iterates over range results" do
    query = Versions::V2::RateQuery.new("from" => range_start, "to" => range_end)
    records = []
    query.each { |r| records << r }

    _(records).wont_be_empty
    _(records.first).must_include(:date)
    _(records.first).must_include(:rate)
  end

  it "returns rates in alphabetical order by quote" do
    get "/rates"

    _(last_response).must_be(:ok?)
    quotes = json.map { |r| r["quote"] }

    _(quotes).must_equal(quotes.sort)
  end

  it "excludes pegged currencies when providers filter is set" do
    get "/rates?providers=ecb"

    _(last_response).must_be(:ok?)
    quotes = json.map { |r| r["quote"] }

    _(quotes).wont_include("BMD")
    _(quotes).wont_include("FKP")
    _(quotes).wont_include("BTN")
  end

  it "filters pegged currencies by quotes param" do
    get "/rates?quotes=USD,BMD"

    _(last_response).must_be(:ok?)
    quotes = json.map { |r| r["quote"] }.uniq.sort

    _(quotes).must_equal(["BMD", "USD"])
  end

  it "returns providers" do
    get "/providers"

    _(last_response).must_be(:ok?)
    assert_conform_schema(200)
    ecb = json.find { |p| p["key"] == "ECB" }

    _(ecb["name"]).must_equal("European Central Bank")
    _(ecb["start_date"]).wont_be_nil
    _(ecb["end_date"]).wont_be_nil
    _(ecb["currencies"]).must_include("USD")
  end

  it "returns structured provider metadata" do
    get "/providers"

    ecb = json.find { |p| p["key"] == "ECB" }

    _(ecb["rate_type"]).must_equal("reference rate")
    _(ecb["pivot_currency"]).must_equal("EUR")
    _(ecb["country_code"]).must_equal("EU")
    _(ecb).wont_include("description")
  end

  it "excludes providers without rates" do
    get "/providers"

    keys = json.map { |p| p["key"] }
    without_rates = (Provider.all.map(&:key) - Rate.distinct.select_map(:provider)).sample

    _(keys).wont_include(without_rates)
  end

  it "expands pegged currencies in rates" do
    get "/rates?base=EUR"

    _(last_response).must_be(:ok?)
    quotes = json.map { |r| r["quote"] }

    _(quotes).must_include("BMD")
  end

  it "derives correct rate for pegged currency" do
    get "/rates?base=EUR"

    usd = json.find { |r| r["quote"] == "USD" }
    bmd = json.find { |r| r["quote"] == "BMD" }

    _(bmd["rate"]).must_equal(usd["rate"])
  end

  it "derives correct rate for non-1:1 peg" do
    get "/rates?base=EUR"

    usd = json.find { |r| r["quote"] == "USD" }
    ang = json.find { |r| r["quote"] == "ANG" }

    expected = (usd["rate"] * 1.79)

    _(ang["rate"]).must_be_close_to(expected, 0.01)
  end

  it "expands GBP-pegged currencies" do
    get "/rates?base=EUR"

    quotes = json.map { |r| r["quote"] }

    _(quotes).must_include("FKP")
    _(quotes).must_include("GGP")
  end

  it "expands INR-pegged currencies" do
    get "/rates?base=EUR"

    quotes = json.map { |r| r["quote"] }

    _(quotes).must_include("BTN")
  end

  it "resolves pegged base currency" do
    get "/rates?base=BMD"

    _(last_response).must_be(:ok?)
    _(json).wont_be_empty
    _(json.first["base"]).must_equal("BMD")

    eur = json.find { |r| r["quote"] == "EUR" }

    _(eur).wont_be_nil
    _(eur["rate"]).must_be_kind_of(Float)
  end

  it "returns anchor currency as quote when base is pegged" do
    get "/rate/GGP/GBP"

    _(last_response).must_be(:ok?)
    _(json["base"]).must_equal("GGP")
    _(json["quote"]).must_equal("GBP")
    _(json["rate"]).must_equal(1.0)
  end

  it "returns anchor currency with non-unity peg rate" do
    get "/rate/ANG/USD"

    _(last_response).must_be(:ok?)
    _(json["base"]).must_equal("ANG")
    _(json["quote"]).must_equal("USD")
    _(json["rate"]).must_be_close_to(1.0 / 1.79, 0.001)
  end

  it "resolves pegged base with providers filter" do
    get "/rates?base=BMD&providers=ecb"

    _(last_response).must_be(:ok?)
    _(json).wont_be_empty
    _(json.first["base"]).must_equal("BMD")
  end

  it "includes pegged currencies in currencies list" do
    get "/currencies?scope=all"

    _(last_response).must_be(:ok?)
    bmd = json.find { |c| c["iso_code"] == "BMD" }

    _(bmd).wont_be_nil
    _(bmd["name"]).must_equal("Bermudian Dollar")
    _(bmd["start_date"]).wont_be_nil
    _(bmd["end_date"]).wont_be_nil
  end

  it "returns peg metadata for pegged currency detail" do
    get "/currency/bmd"

    _(last_response).must_be(:ok?)
    _(json["iso_code"]).must_equal("BMD")
    _(json["peg"]).wont_be_nil
    _(json["peg"]["base"]).must_equal("USD")
    _(json["peg"]["rate"]).must_equal(1.0)
    _(json["peg"]["authority"]).must_equal("Bermuda Monetary Authority")
  end

  it "always includes providers for pegged currency detail" do
    get "/currency/bmd"

    _(last_response).must_be(:ok?)
    _(json["providers"]).must_be_kind_of(Array)
  end

  it "returns providers for non-pegged currency detail" do
    get "/currency/usd"

    _(last_response).must_be(:ok?)
    _(json["providers"]).must_be_kind_of(Array)
    _(json).wont_include("peg")
  end
end
