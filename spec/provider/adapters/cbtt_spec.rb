# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/cbtt"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe CBTT do
      before do
        VCR.insert_cassette("cbtt", match_requests_on: [:method, :uri])
      end

      after { VCR.eject_cassette }

      let(:adapter) { CBTT.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 2), upto: Date.new(2026, 3, 6))

        _(dataset).wont_be_empty
        _(dataset.map { |r| r[:date] }.uniq).must_equal((Date.new(2026, 3, 2)..Date.new(2026, 3, 6)).to_a)
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 2), upto: Date.new(2026, 3, 6))
        sample = dataset.select { |r| r[:date] == Date.new(2026, 3, 2) }

        _(sample.size).must_be(:>, 1)
        _(sample.map { |r| r[:base] }).must_include("USD")
        _(sample.map { |r| r[:quote] }.uniq).must_equal(["TTD"])
      end

      it "parses the mid of buying and selling" do
        json = <<~JSON
          {"cbttdailyforexrates":[{"famedate":"2026-09-08","USD_Buying":"6.7039","USD_Selling":"6.7990"}]}
        JSON
        records = adapter.parse(json)

        _(records.length).must_equal(1)
        _(records.first[:date]).must_equal(Date.new(2026, 9, 8))
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("TTD")
        _(records.first[:rate]).must_equal(6.75145)
      end

      it "maps Euro to EUR" do
        json = <<~JSON
          {"cbttdailyforexrates":[{"famedate":"2026-09-08","Euro_Buying":"8.5534","Euro_Selling":"8.6140"}]}
        JSON
        records = adapter.parse(json)

        _(records.map { |r| r[:base] }).must_equal(["EUR"])
      end

      it "skips a currency when either leg is missing" do
        json = <<~JSON
          {"cbttdailyforexrates":[{"famedate":"2026-09-08","CHF_Buying":null,"CHF_Selling":"9.0349","USD_Buying":"6.7039","USD_Selling":null}]}
        JSON
        records = adapter.parse(json)

        _(records).must_be_empty
      end

      it "skips zero rates" do
        json = <<~JSON
          {"cbttdailyforexrates":[{"famedate":"1996-08-01","GYD_Buying":"0.0000","GYD_Selling":"0.0000"}]}
        JSON
        records = adapter.parse(json)

        _(records).must_be_empty
      end

      it "handles a span with no rows" do
        json = <<~JSON
          {"cbttdailyforexrates":{"famedate":"2027-2027","USD_Buying":null,"USD_Selling":null}}
        JSON
        records = adapter.parse(json)

        _(records).must_be_empty
      end

      it "raises on an unexpected payload" do
        _ { adapter.parse('{"code":"rest_no_route"}') }.must_raise(RuntimeError)
      end
    end
  end
end
