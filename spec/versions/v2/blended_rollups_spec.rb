# frozen_string_literal: true

require_relative "../../helper"
require "blended_weekly_rate"
require "blended_monthly_rate"
require "versions/v2/rate_query"
require "oj"

transition_pairs = [
  ["LB", "LTL", "USD", 0.4], ["LB", "LTL", "EUR", 0.3],
  ["LB", "EUR", "USD", 1.2], ["LB", "EUR", "GBP", 0.8],
  ["BCBO", "XAU", "USD", 1200.0], ["BCBO", "USD", "BOB", 6.9],
  ["ECB", "USD", "EUR", 0.82], ["BOC", "USD", "EUR", 0.83],
  ["BOJ", "USD", "EUR", 0.84], ["TST", "USD", "EUR", 20.0],
  ["UST", "USD", "CHF", 999.0],
]
transition_bases = ["USD", "EUR", "LTL", "AED"]

[["week", BlendedWeeklyRate], ["month", BlendedMonthlyRate]].each do |group, model|
  describe "#{group} materialized responses" do
    let(:shape) do
      { base: "CHF", quotes: "USD,EUR,GBP,JPY", from: (Fixtures.latest_date - 370).to_s,
        to: Fixtures.latest_date.to_s, group:, }
    end

    def body(params, live: false)
      query = Versions::V2::RateQuery.new(params)
      query.force_live = live
      Oj.dump(query.to_a, mode: :compat)
    end

    it "serves the exact live bytes without running the blender" do
      expected = body(shape, live: true)
      model.rebuild
      calls = 0
      original = Blender.method(:new)
      actual = Blender.stub(:new, lambda { |*args, **kwargs|
        calls += 1
        original.call(*args, **kwargs)
      },) { body(shape) }

      _(actual).must_equal(expected)
      _(calls).must_equal(0)
    end

    it "preserves base conversion, quote filters, identities and missing currencies" do
      model.rebuild
      [
        {}, { base: "USD" }, { base: "AED" }, { base: "ZAR" },
        { quotes: "CHF" }, { quotes: "CHF,EUR" }, { quotes: "ZAR" }, { quotes: nil },
        { from: (Fixtures.latest_date - 730).to_s },
        { from: Fixtures.latest_date.to_s, to: Fixtures.latest_date.to_s },
        { from: Fixtures.recent_sunday.to_s }, { to: nil },
        { from: (Date.today + 10).to_s, to: (Date.today + 30).to_s },
      ].each do |overrides|
        params = shape.merge(overrides).compact

        _(body(params)).must_equal(body(params, live: true), params.inspect)
      end
    end

    it "falls back for missing first, interior and last buckets" do
      model.rebuild
      dates = model.source.between(Date.parse(shape[:from])..Date.parse(shape[:to]))
        .select(:bucket_date).distinct.order(:bucket_date).select_map(:bucket_date)
      [dates.first, dates[dates.size / 2], dates.last].each do |date|
        model.dataset.where(bucket_date: date).delete

        _(body(shape)).must_equal(body(shape, live: true))
        model.refresh([date])
      end
    end

    it "retains empty-bucket snap-back rather than returning an older blend" do
      model.source.dataset.delete
      d1 = Date.new(2024, 1, 1)
      d2 = d1 + 40
      model.source.dataset.multi_insert([
        { bucket_date: d1, provider: "ECB", base: "USD", quote: "EUR", rate: 0.8 },
        { bucket_date: d2, provider: "ECB", base: "EUR", quote: "JPY", rate: 160.0 },
      ])
      model.rebuild
      params = { from: d2.to_s, to: (d2 + 1).to_s, group:, base: "USD" }

      _(body(params, live: true)).must_equal("[]")
      _(body(params)).must_equal("[]")
    end

    it "preserves transition bridges, metals and consensus with the full provider inputs" do
      model.source.dataset.delete
      date = Date.new(2015, 1, 1)
      rows = transition_pairs.map do |provider, base, quote, rate|
        { bucket_date: date, provider:, base:, quote:, rate: }
      end
      model.source.dataset.multi_insert(rows)
      model.rebuild
      transition_bases.each do |base|
        params = { from: date.to_s, to: date.to_s, base:, group: }

        _(body(params)).must_equal(body(params, live: true))
        params[:quotes] = "EUR,GBP,XAU,CHF"

        _(body(params)).must_equal(body(params, live: true))
      end
      records = JSON.parse(body({ from: date.to_s, to: date.to_s, base: "USD", group: }))

      _(records.any? { |row| row["quote"] == "XAU" }).must_equal(true)
      _(records.any? { |row| row["quote"] == "CHF" }).must_equal(false)
      _(records.find { |row| row["quote"] == "EUR" }["rate"]).must_be(:<, 1)
    end

    it "keeps filtered, expanded and forced-live queries on their original path" do
      model.rebuild
      [shape.merge(providers: "ECB"), shape.merge(providers: "ECB,BOC"),
       shape.merge(expand: "providers"),].each do |params|
        expected = body(params, live: true)
        actual = model.stub(:read, ->(*) { raise "unexpected materialized read" }) { body(params) }

        _(actual).must_equal(expected)
      end
      model.stub(:read, ->(*) { raise "unexpected materialized read" }) { body(shape, live: true) }
    end

    it "keeps snapshots with a group parameter on the daily path" do
      params = { date: Fixtures.latest_date.to_s, group:, base: "CHF", quotes: "EUR" }
      expected = body(params.except(:group))
      actual = model.stub(:read, ->(*) { raise "unexpected materialized read" }) { body(params) }

      _(actual).must_equal(expected)
    end

    it "preserves source bucket dates and duplicates across chunk boundaries" do
      # Both spans cross their existing chunk sizes (21 months weekly, 84 monthly), with source dates on either side of
      # the year boundary. A sparse source may snap back to the same bucket in several successive chunks.
      model.source.dataset.delete
      dates = [Date.new(2005, 12, 29), Date.new(2006, 1, 5), Date.new(2020, 1, 2)]
      dates.each do |date|
        model.source.dataset.insert(bucket_date: date, provider: "ECB", base: "USD", quote: "EUR", rate: 0.8)
      end
      model.rebuild
      params = { from: "2005-12-30", to: "2020-01-03", base: "USD", quotes: "EUR", group: }

      expected = body(params, live: true)

      _(body(params)).must_equal(expected)
      _(JSON.parse(expected).size).must_be(:>, dates.size)
    end
  end
end

require "rack/test"
require "versions/v2"

describe "Grouped HTTP responses" do
  include Rack::Test::Methods

  let(:app) { Versions::V2.freeze }

  formats = [["/rates", "application/json"], ["/rates", "application/x-ndjson"],
             ["/rates.csv", "text/csv"],]

  [["week", BlendedWeeklyRate], ["month", BlendedMonthlyRate]].each do |group, model|
    it "streams unchanged #{group} JSON, NDJSON and CSV from stored blends" do
      params = { "base" => "CHF", "quotes" => "USD,EUR,GBP,JPY", "from" => (Fixtures.latest_date - 370).to_s,
                 "to" => Fixtures.latest_date.to_s, "group" => group, }
      formats.each do |path, accept|
        model.dataset.delete
        get path, params, "HTTP_ACCEPT" => accept

        _(last_response.status).must_equal(200)
        expected = last_response.body
        etag = last_response.headers["etag"]
        model.rebuild

        Blender.stub(:new, ->(*) { raise "unexpected live blend" }) do
          get path, params, "HTTP_ACCEPT" => accept

          _(last_response.status).must_equal(200)
          _(last_response.body).must_equal(expected)
          _(last_response.headers["etag"]).must_equal(etag)
        end
      end
    end
  end
end
