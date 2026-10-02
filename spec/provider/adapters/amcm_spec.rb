# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/amcm"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe AMCM do
      before do
        VCR.insert_cassette("amcm", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { AMCM.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 18), upto: Date.new(2026, 5, 22))

        _(dataset).wont_be_empty
      end

      it "uses MOP as the quote currency" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 18), upto: Date.new(2026, 5, 22))

        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["MOP"])
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 18), upto: Date.new(2026, 5, 22))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "parses JSON with correct base, quote, and rate" do
        json = <<~JSON
          {"message":"OK","data":[
            {"id":1,"date":"2026-05-22 00:00:00","currency":"USD","unit":1.0,"usdMean":"8.0705","usdMeanValue":8.07050000,"bid":"7.8349"}
          ]}
        JSON

        records = adapter.parse(json)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("MOP")
        _(records.first[:rate]).must_equal(8.0705)
        _(records.first[:date]).must_equal(Date.new(2026, 5, 22))
      end

      it "divides by unit multiplier for per-100 currencies" do
        json = <<~JSON
          {"message":"OK","data":[
            {"id":1,"date":"2026-05-22 00:00:00","currency":"JPY","unit":100.0,"usdMean":"5.0727","usdMeanValue":5.07270000,"bid":"159.09"}
          ]}
        JSON

        records = adapter.parse(json)

        _(records.first[:base]).must_equal("JPY")
        _(records.first[:rate]).must_be_close_to(0.050727, 0.000001)
      end

      it "aliases ECU to XEU" do
        json = <<~JSON
          {"message":"OK","data":[
            {"id":1,"date":"1995-01-03 00:00:00","currency":"ECU","unit":1.0,"usdMean":"10.1234","usdMeanValue":10.12340000,"bid":"0"}
          ]}
        JSON

        records = adapter.parse(json)

        _(records.first[:base]).must_equal("XEU")
      end

      it "skips non-currency LIQ entries" do
        json = <<~JSON
          {"message":"OK","data":[
            {"id":1,"date":"2026-05-22 00:00:00","currency":"LIQ","unit":0.0,"usdMean":"0.0000","usdMeanValue":0.0,"bid":"1.85"}
          ]}
        JSON

        records = adapter.parse(json)

        _(records).must_be_empty
      end

      it "skips the LIQ interest rate where the archive files it like a currency" do
        json = <<~JSON
          {"message":"OK","data":[
            {"id":1,"date":"1994-03-31 14:00:00","currency":"LIQ","unit":1.0,"usdMean":"3.1250","usdMeanValue":3.12500000,"bid":"3.125"},
            {"id":2,"date":"2012-10-19 14:00:00","currency":"LIQ","unit":1.0,"usdMean":"0.0100","usdMeanValue":0.01000000,"bid":"0.01"},
            {"id":3,"date":"2012-10-19 14:00:00","currency":"HKD","unit":1.0,"usdMean":"1.0300","usdMeanValue":1.03000000,"bid":"0"}
          ]}
        JSON

        records = adapter.parse(json)

        _(records.map { |r| r[:base] }).must_equal(["HKD"])
      end

      it "skips records with non-positive rates" do
        json = <<~JSON
          {"message":"OK","data":[
            {"id":1,"date":"2026-05-22 00:00:00","currency":"USD","unit":1.0,"usdMean":"0.0000","usdMeanValue":0.0,"bid":"0"}
          ]}
        JSON

        records = adapter.parse(json)

        _(records).must_be_empty
      end
    end
  end
end
