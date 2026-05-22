# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/cbn"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe CBN do
      before do
        VCR.insert_cassette("cbn", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { CBN.new }

      it "fetches rates filtered by date range" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 14), upto: Date.new(2026, 5, 21))

        _(dataset).wont_be_empty
        _(dataset.map { |r| r[:date] }.min).must_be(:>, Date.new(2026, 5, 14))
        _(dataset.map { |r| r[:date] }.max).must_be(:<=, Date.new(2026, 5, 21))
      end

      it "stamps NGN as the quote currency" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 14), upto: Date.new(2026, 5, 21))

        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["NGN"])
      end

      it "returns multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 14), upto: Date.new(2026, 5, 21))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 5)
      end

      it "excludes WAUA and SDR composite units" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 14), upto: Date.new(2026, 5, 21))
        bases = dataset.map { |r| r[:base] }.uniq

        _(bases).wont_include("WAUA")
        _(bases).wont_include("SDR")
        _(bases).wont_include("XDR")
      end

      it "returns USD/NGN in a plausible range for May 2026" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 20), upto: Date.new(2026, 5, 21))
        usd = dataset.find { |r| r[:base] == "USD" && r[:date] == Date.new(2026, 5, 21) }

        _(usd).wont_be_nil
        _(usd[:rate]).must_be_close_to(1372, 50)
      end

      it "parses centralrate as the mid" do
        json = <<~JSON
          [
            {"id":1,"currency":"US DOLLAR","ratedate":"2026-05-21","buyingrate":"1371.3079","centralrate":"1371.8079","sellingrate":"1372.3079"}
          ]
        JSON

        records = adapter.parse(json)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("NGN")
        _(records.first[:rate]).must_equal(1371.8079)
        _(records.first[:date]).must_equal(Date.new(2026, 5, 21))
      end

      it "maps currency name variants to ISO codes" do
        json = <<~JSON
          [
            {"id":1,"currency":"YEN","ratedate":"2026-05-21","centralrate":"8.6147"},
            {"id":2,"currency":"JAPANESE YEN","ratedate":"2012-08-16","centralrate":"1.9589"},
            {"id":3,"currency":"POUND STERLING","ratedate":"2012-08-28","centralrate":"245.3740"},
            {"id":4,"currency":"POUNDS STERLING","ratedate":"2026-05-21","centralrate":"1839.5944"},
            {"id":5,"currency":"DANISH KRONA","ratedate":"2026-05-21","centralrate":"212.7461"},
            {"id":6,"currency":"DANISH KRONER","ratedate":"2012-08-28","centralrate":"26.1615"},
            {"id":7,"currency":"YUAN/RENMINBI","ratedate":"2026-05-21","centralrate":"201.5942"},
            {"id":8,"currency":"CFA","ratedate":"2026-05-21","centralrate":"2.4200"}
          ]
        JSON

        records = adapter.parse(json)
        bases = records.map { |r| r[:base] }

        _(bases).must_include("JPY")
        _(bases).must_include("GBP")
        _(bases).must_include("DKK")
        _(bases).must_include("CNY")
        _(bases).must_include("XOF")
        _(records.count { |r| r[:base] == "JPY" }).must_equal(2)
        _(records.count { |r| r[:base] == "GBP" }).must_equal(2)
        _(records.count { |r| r[:base] == "DKK" }).must_equal(2)
      end

      it "tolerates whitespace and tab variants in currency names" do
        json = <<~JSON
          [
            {"id":1,"currency":"EURO ","ratedate":"2026-05-21","centralrate":"1590.2"},
            {"id":2,"currency":"SWISS FRANC\\t","ratedate":"2026-05-21","centralrate":"1737.5"}
          ]
        JSON

        records = adapter.parse(json)

        _(records.map { |r| r[:base] }).must_equal(["EUR", "CHF"])
      end

      it "excludes WAUA, SDR, and unknown currency names at parse time" do
        json = <<~JSON
          [
            {"id":1,"currency":"WAUA","ratedate":"2026-05-21","centralrate":"1876.66"},
            {"id":2,"currency":"SDR","ratedate":"2026-05-21","centralrate":"1884.04"},
            {"id":3,"currency":"NAIRA","ratedate":"2012-05-31","centralrate":"155.25"},
            {"id":4,"currency":"POESO","ratedate":"2009-07-01","centralrate":"27.62"},
            {"id":5,"currency":"US DOLLAR","ratedate":"2026-05-21","centralrate":"1371.81"}
          ]
        JSON

        records = adapter.parse(json)

        _(records.map { |r| r[:base] }).must_equal(["USD"])
      end

      it "skips entries with missing or zero centralrate" do
        json = <<~JSON
          [
            {"id":1,"currency":"US DOLLAR","ratedate":"2026-05-21","centralrate":null},
            {"id":2,"currency":"EURO","ratedate":"2026-05-21","centralrate":""},
            {"id":3,"currency":"YEN","ratedate":"2026-05-21","centralrate":"0"},
            {"id":4,"currency":"SWISS FRANC","ratedate":"2026-05-21","centralrate":"1737.5"}
          ]
        JSON

        records = adapter.parse(json)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("CHF")
      end
    end
  end
end
