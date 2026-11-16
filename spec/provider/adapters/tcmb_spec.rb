# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/tcmb"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe TCMB do
      before do
        ENV["TCMB_API_KEY"] ||= "test"
        VCR.insert_cassette("tcmb", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { TCMB.new }
      let(:dataset) { adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 22)) }

      it "fetches rates since a date" do
        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "tags forex and banknote rates with their datasets" do
        _(dataset.map { |r| r[:dataset] }.uniq.sort).must_equal(["TCMB.BANKNOTE", "TCMB.FOREX"])
      end

      it "skips currencies without banknote rates" do
        banknotes = dataset.select { |r| r[:dataset] == "TCMB.BANKNOTE" }.map { |r| r[:base] }.uniq
        forex = dataset.select { |r| r[:dataset] == "TCMB.FOREX" }.map { |r| r[:base] }.uniq

        _(banknotes).must_include("USD")
        _(banknotes).wont_include("CNY")
        _(forex).must_include("CNY")
      end

      it "keeps every digit of the mid, whatever the magnitude" do
        # JPY is quoted per 100 units, so its rate lands three orders of magnitude below the rest of the feed. Rounding
        # to a fixed number of decimal places clipped it: buy 28.0072 and sell 28.1927 average to 28.09995, which is
        # 0.2809995 per yen, and a round(4) stored 0.281.
        record = dataset.find do |r|
          r[:dataset] == "TCMB.FOREX" && r[:base] == "JPY" && r[:date] == Date.new(2026, 3, 2)
        end

        _(record[:rate]).must_equal(0.2809995)
      end

      it "parses banknote buying and selling into a mid per unit" do
        json = {
          items: [{
            "Tarih" => "02-03-2026",
            "TP_DK_USD_A_YTL" => "43.80000000",
            "TP_DK_USD_S_YTL" => "43.87890000",
            "TP_DK_USD_A_EF_YTL" => "43.76930000",
            "TP_DK_USD_S_EF_YTL" => "43.94470000",
            "TP_DK_JPY_A_EF_YTL" => "27.90360000",
            "TP_DK_JPY_S_EF_YTL" => "28.29980000",
            "TP_DK_CNY_A_EF_YTL" => "0.00000000",
            "TP_DK_CNY_S_EF_YTL" => "0.00000000",
          }],
        }.to_json
        records = adapter.parse(json).to_h { |r| [[r[:dataset], r[:base]], r] }

        _(records.keys).must_equal([["TCMB.FOREX", "USD"], ["TCMB.BANKNOTE", "JPY"], ["TCMB.BANKNOTE", "USD"]])
        _(records[["TCMB.BANKNOTE", "USD"]][:rate]).must_equal(43.857)
        _(records[["TCMB.BANKNOTE", "USD"]][:quote]).must_equal("TRY")
        _(records[["TCMB.BANKNOTE", "JPY"]][:rate]).must_equal(0.2810170)
        _(records[["TCMB.BANKNOTE", "JPY"]][:bid]).must_equal(0.279036)
      end
    end
  end
end
