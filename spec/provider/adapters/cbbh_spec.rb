# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/cbbh"
require "rack/test"
require "versions/v2"

class Provider
  module Adapters
    describe CBBH do
      include Rack::Test::Methods

      let(:app) { Versions::V2.freeze }
      let(:adapter) { CBBH.new }
      before { VCR.insert_cassette("cbbh", match_requests_on: [:method, :uri]) }
      after { VCR.eject_cassette }

      it "fetches published effective dates in native foreign-to-BAM direction" do
        rows = adapter.fetch(after: Date.new(2026, 9, 24), upto: Date.new(2026, 9, 26))

        _(rows.size).must_equal(51)
        _(rows.map do |r|
          r[:date]
        end.uniq).must_equal([Date.new(2026, 9, 24), Date.new(2026, 9, 25), Date.new(2026, 9, 26)])
        _(rows.all? { |r| r[:quote] == "BAM" }).must_equal(true)
        usd = rows.find { |r| r[:date] == Date.new(2026, 9, 25) && r[:base] == "USD" }
        jpy = rows.find { |r| r[:date] == Date.new(2026, 9, 25) && r[:base] == "JPY" }

        _(usd[:rate]).must_equal(BigDecimal("1.720621"))
        _(jpy[:rate]).must_equal(BigDecimal("0.01083142"))
        _(rows.map { |r| r[:base] }).must_include("XDR")
      end

      it "backfills the earliest lists and preserves historical units through the provider API" do
        rows = adapter.fetch(after: Date.new(1998, 1, 6), upto: Date.new(1998, 1, 8))
        batch = ->(**, &block) { block.call(rows) }
        CBBH.stub(:fetch_each, batch) { Provider["CBBH"].backfill(after: Date.new(1998, 1, 6)) }
        get "/rates", providers: "CBBH", date: "1998-01-06", base: "ATS", quotes: "BAM"

        _(last_response).must_be(:ok?)
        _(JSON.parse(last_response.body).first.fetch("rate")).must_equal(0.1421367521)
        _(rows.map { |r| r[:date] }.uniq).must_equal([Date.new(1998, 1, 6), Date.new(1998, 1, 8)])
        _(rows.map { |r| r[:base] }).must_include("ESB")
        _(rows.map { |r| r[:base] }).must_include("XBA")
        _(Provider["CBBH"].unknown_currencies).must_be_empty
        _(Rate.where(provider: "CBBH", base: "ESB").blendable.count).must_equal(0)
      end

      it "confirms an empty Sunday-Monday range using the last actual list date" do
        _(adapter.fetch(after: Date.new(2025, 1, 5), upto: Date.new(2025, 1, 6))).must_be_empty
      end

      it "does not hide a failed period export when the daily endpoint has a list in the range" do
        WebMock.stub_request(:get, CBBH::PERIOD_URL).with(query: { dateFrom: "2026-09-25", dateTo: "2026-09-25" })
          .to_return(body: '"Problem with export"')

        _ { adapter.fetch(after: Date.new(2026, 9, 25), upto: Date.new(2026, 9, 25)) }.must_raise(RuntimeError)
      end

      it "skips reversed and wholly pre-coverage ranges without requesting data" do
        _(adapter.fetch(after: Date.new(2025, 1, 2), upto: Date.new(2025, 1, 1))).must_be_empty
        _(adapter.fetch(after: Date.new(1997, 1, 1), upto: Date.new(1998, 1, 5))).must_be_empty
      end

      describe "#parse" do
        def entry(code, units, middle, **extra)
          { "AlphaCode" => code, "Units" => units, "Middle" => middle, "Buy" => "1", "Sell" => "3" }.merge(extra)
        end

        def list(*items)
          [{ "Date" => "2025-01-04T00:00:00", "CurrencyExchangeItems" => items }]
        end

        it "uses the published middle and decimal arithmetic for units and locale decimals" do
          rows = adapter.parse(list(entry("JPY", "100", "1,23456789"), entry("USD", "1", "1.9876543212345")))

          _(rows).must_equal([
            { date: Date.new(2025, 1, 4), base: "JPY", quote: "BAM", rate: BigDecimal("0.0123456789") },
            { date: Date.new(2025, 1, 4), base: "USD", quote: "BAM", rate: BigDecimal("1.9876543212345") },
          ])
        end

        it "retains source legacy codes instead of relabelling historical magnitudes" do
          rows = adapter.parse(list(entry("TRL", "100", "0.000107"), entry("ESB", "100", "1.18233618")))

          _(rows.map { |r| [r[:base], r[:rate]] }).must_equal([
            ["TRL", BigDecimal("0.00000107")], ["ESB", BigDecimal("0.0118233618")],
          ])
        end

        it "skips empty placeholders, nonpositive values, invalid units, and invalid codes" do
          rows = adapter.parse(list(entry("KWD", "", ""), entry("USD", "1", "0"), entry("JPY", "0", "1"),
                                    entry("EUR", "1", "NaN"), entry("CHF", "1", "-1"), entry("?", "1", "2"),))

          _(rows).must_be_empty
        end

        it "raises on malformed export structure and dates" do
          _ { adapter.parse("Problem with export") }.must_raise(RuntimeError)
          _ { adapter.parse({}) }.must_raise(RuntimeError)
          _ { adapter.parse([{ "Date" => "2025-01-04" }]) }.must_raise(RuntimeError)
          _ { adapter.parse([{ "Date" => "invalid", "CurrencyExchangeItems" => [] }]) }.must_raise(Date::Error)
        end
      end
    end
  end
end
