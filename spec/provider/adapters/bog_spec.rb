# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bog"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BOG do
      before do
        VCR.insert_cassette("bog", match_requests_on: [:method, :uri, :body])
      end

      after { VCR.eject_cassette }

      let(:adapter) { BOG.new }

      def row(date, pair, bid, ask, mid, name = "Currency")
        [date, name, pair, bid, ask, mid]
      end

      it "fetches rates within a window" do
        dataset = adapter.fetch(after: Date.new(2026, 11, 2), upto: Date.new(2026, 11, 6))
        days = dataset.group_by { |r| r[:date] }
        usd = dataset.find { |r| r[:date] == Date.new(2026, 11, 6) && r[:base] == "USD" }

        _(days.keys.sort).must_equal((Date.new(2026, 11, 2)..Date.new(2026, 11, 6)).to_a)
        _(days.values.map(&:size).uniq).must_equal([18])
        _(usd).must_equal({ date: Date.new(2026, 11, 6), base: "USD", quote: "GHS", rate: 11.64, bid: 11.6342,
                            ask: 11.6458, mid: 11.64, })
      end

      it "stores the cedi-base crosses as published" do
        dataset = adapter.fetch(after: Date.new(2026, 11, 2), upto: Date.new(2026, 11, 6))
        crosses = dataset.select { |r| r[:base] == "GHS" }.map { |r| r[:quote] }.uniq.sort

        _(crosses).must_equal(["GMD", "MRO", "NGN", "SLE", "XOF"])
      end

      it "relabels the old cedi across the redenomination" do
        VCR.use_cassette("bog_redenomination", match_requests_on: [:method, :uri, :body], exclusive: true) do
          dataset = adapter.fetch(after: Date.new(2007, 6, 22), upto: Date.new(2007, 7, 9))
          usd = dataset.select { |r| r[:base] == "USD" }.to_h { |r| [r[:date], [r[:quote], r[:rate]]] }

          _(usd[Date.new(2007, 6, 27)]).must_equal(["GHC", 9264.73])
          _(usd[Date.new(2007, 7, 9)]).must_equal(["GHS", 0.9266])
          _(dataset.select { |r| r[:base] == "GHS" }.map { |r| r[:date] }.uniq).must_equal([Date.new(2007, 7, 9)])
        end
      end

      it "parses thousands separators" do
        records = adapter.parse("data" => [row("02 Jan 1996", "USDGHS", "1,434.0900", "1,461.6400", "1,447.8650")])

        _(records.first).must_equal({ date: Date.new(1996, 1, 2), base: "USD", quote: "GHC", rate: 1447.865,
                                      bid: 1434.09, ask: 1461.64, mid: 1447.865, })
      end

      it "skips cedi-base crosses before the redenomination" do
        records = adapter.parse("data" => [
          row("27 Jun 2007", "GHSNGN", "70.8100", "71.5900", "71.2000"),
          row("09 Jul 2007", "GHSNGN", "139.7200", "141.2200", "140.4700"),
        ])

        _(records.map { |r| [r[:date], r[:base], r[:quote]] }).must_equal([[Date.new(2007, 7, 9), "GHS", "NGN"]])
      end

      it "moves the leone to its successor when BoG does" do
        records = adapter.parse("data" => [
          row("10 Sep 2026", "GHSSLL", "2,082.1608", "2,136.6226", "2,109.3917"),
          row("11 Sep 2026", "GHSSLL", "2.0810", "2.1354", "2.1082"),
        ])

        _(records.map { |r| [r[:quote], r[:rate]] }).must_equal([["SLL", 2109.3917], ["SLE", 2.1082]])
      end

      it "skips rows without a rate" do
        records = adapter.parse("data" => [row("02 Jan 1996", "ZARGHS", "0.0000", "0.0000", "0.0000")])

        _(records).must_be_empty
      end

      it "keeps a published mid when one side is zero" do
        record = adapter.parse("data" => [row("21 Mar 2002", "AUDGHS", "0.0000", "4,086.9400", "2,043.4700")]).first

        _(record.slice(:rate, :bid, :ask, :mid)).must_equal({ rate: 2043.47, bid: nil, ask: 4086.94, mid: 2043.47 })
      end

      it "skips the Ecowas row" do
        records = adapter.parse("data" => [row("06 Nov 2026", "WAUGHS", "0.0126", "0.0126", "0.0126", "Ecowas")])

        _(records).must_be_empty
      end

      it "raises without a data array" do
        _ { adapter.parse("{}") }.must_raise(RuntimeError)
      end
    end
  end
end
