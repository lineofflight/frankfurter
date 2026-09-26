# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/inforeuro"
require "rack/test"
require "versions/v2"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe INFOREURO do
      include Rack::Test::Methods

      let(:app) { Versions::V2.freeze }
      before { VCR.insert_cassette("inforeuro", match_requests_on: [:method, :uri]) }
      after { VCR.eject_cassette }

      let(:adapter) { INFOREURO.new }

      it "fetches distinct monthly observations in published EUR orientation" do
        rows = adapter.fetch(after: Date.new(2026, 8, 1), upto: Date.new(2026, 9, 1))

        _(rows.map { |r| r[:date] }.uniq).must_equal([Date.new(2026, 8, 1), Date.new(2026, 9, 1)])
        _(rows.all? { |r| r[:base] == "EUR" }).must_equal(true)
        _(rows.map { |r| [r[:date], r[:quote]] }.uniq.size).must_equal(rows.size)
        _(rows.find { |r| r[:date] == Date.new(2026, 9, 1) && r[:quote] == "USD" }[:rate]).must_equal(1.1643)
        _(rows.none? { |r| r[:quote] == "EUR" }).must_equal(true)
      end

      it "clips both bounds to effective dates rather than repeating a rate within its month" do
        rows = adapter.fetch(after: Date.new(2026, 8, 2), upto: Date.new(2026, 9, 15))

        _(rows.map { |r| r[:date] }.uniq).must_equal([Date.new(2026, 9, 1)])
        _(adapter.fetch(after: Date.new(2026, 8, 2), upto: Date.new(2026, 8, 31))).must_be_empty
      end

      it "clips requests to the published archive and skips historical nulls" do
        rows = adapter.fetch(after: Date.new(1994, 1, 1), upto: Date.new(1994, 3, 1))

        _(rows.size).must_equal(155)
        _(rows.all? { |r| r[:base] == "XEU" && r[:date] == Date.new(1994, 3, 1) }).must_equal(true)
        _(rows.find { |r| r[:quote] == "USD" }[:rate]).must_equal(1.12892)
        _(rows.find { |r| r[:quote] == "RUR" }[:rate]).must_equal(1789.34)
        _(rows.none? { |r| r[:quote] == "KGS" }).must_equal(true)
      end

      it "does not request unpublished future months" do
        rows = Date.stub(:today, Date.new(2026, 9, 26)) do
          adapter.fetch(after: Date.new(2026, 9, 1), upto: Date.new(2027, 1, 1))
        end

        _(rows.map { |r| r[:date] }.uniq).must_equal([Date.new(2026, 9, 1)])
      end

      it "steps across December into January and changes ECU to EUR at inception" do
        rows = adapter.fetch(after: Date.new(1998, 12, 1), upto: Date.new(1999, 1, 1))
        RateValidation.reject!(rows)

        _(rows.select { |r| r[:date] == Date.new(1998, 12, 1) }.map { |r| r[:base] }.uniq).must_equal(["XEU"])
        _(rows.select { |r| r[:date] == Date.new(1999, 1, 1) }.map { |r| r[:base] }.uniq).must_equal(["EUR"])
      end

      it "keeps monthly observations out of blends" do
        _(Provider["INFOREURO"].blends?).must_equal(false)
      end

      it "preserves long published rates through ingestion and the provider API" do
        Date.stub(:today, Date.new(2019, 1, 2)) do
          Provider["INFOREURO"].backfill(after: Date.new(2019, 1, 1))
        end
        get "/rates?providers=INFOREURO&date=2019-01-01&base=EUR&quotes=VEF"

        _(last_response).must_be(:ok?)
        _(JSON.parse(last_response.body).first.fetch("rate")).must_equal(57698326.62525)
      end

      it "maps historical source labels to the published monetary units" do
        congo = adapter.fetch(after: Date.new(1999, 2, 1), upto: Date.new(1999, 3, 1))
        angola = adapter.fetch(after: Date.new(2000, 1, 1), upto: Date.new(2000, 3, 1))

        _(congo.select { |r| r[:quote] == "CDF" }.map { |r| r[:rate] }).must_equal([2.89237, 2.74715])
        _(angola.select { |r| r[:quote] == "AOA" }.map { |r| r[:rate] }).must_equal([5.54714, 5.78925, 5.68025])
        _(congo.none? { |r| r[:quote] == "FRC" }).must_equal(true)
        _(angola.none? { |r| r[:quote] == "AOK" }).must_equal(true)
      end

      describe "#parse" do
        let(:date) { Date.new(2026, 9, 1) }

        it "keeps published digits and maps Zimbabwe Gold's source label" do
          rows = adapter.parse('[{"isoA3Code":"ZIG","value":30.5535},{"isoA3Code":"VES","value":350.08868}]', date:)

          _(rows).must_equal([
            { date:, base: "EUR", quote: "ZWG", rate: 30.5535 },
            { date:, base: "EUR", quote: "VES", rate: 350.08868 },
          ])
        end

        it "retains simultaneous predecessor and successor units as published" do
          rows = adapter.parse('[{"isoA3Code":"BYN","value":2.0378},{"isoA3Code":"BYR","value":20378}]',
                               date: Date.new(2017, 1, 1),)

          _(rows.map { |r| [r[:quote], r[:rate]] }).must_equal([["BYN", 2.0378], ["BYR", 20378.0]])
        end

        it "skips null and non-positive values" do
          rows = adapter.parse('[{"isoA3Code":"TRY","value":null},{"isoA3Code":"USD","value":0},' \
                               '{"isoA3Code":"GBP","value":-1}]', date:,)

          _(rows).must_be_empty
        end

        it "rejects a semantic error instead of silently returning no rates" do
          _ { adapter.parse('{"error":"unavailable"}', date:) }.must_raise(RuntimeError)
          _ { adapter.parse("[]", date:) }.must_raise(RuntimeError)
          _ { adapter.parse('[{"isoA3Code":"USD"}]', date:) }.must_raise(KeyError)
        end
      end
    end
  end
end
