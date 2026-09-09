# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/cbssc"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe CBSSC do
      before do
        VCR.insert_cassette("cbssc", match_requests_on: [:method, :uri])
      end

      after { VCR.eject_cassette }

      let(:adapter) { CBSSC.new }

      it "fetches rates from the workbook" do
        dataset = adapter.fetch(after: Date.new(2026, 8, 3), upto: Date.new(2026, 8, 7))

        _(dataset).wont_be_empty
        _(dataset.map { |r| r[:date] }.uniq.size).must_equal(5)
      end

      it "quotes USD, EUR and GBP in SCR" do
        dataset = adapter.fetch(after: Date.new(2026, 8, 3), upto: Date.new(2026, 8, 7))

        _(dataset.map { |r| r[:base] }.uniq.sort).must_equal(["EUR", "GBP", "USD"])
        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["SCR"])
      end

      it "returns USD/SCR in a plausible range" do
        dataset = adapter.fetch(after: Date.new(2026, 8, 3), upto: Date.new(2026, 8, 7))
        usd = dataset.find { |r| r[:base] == "USD" }

        _(usd[:rate]).must_be(:>, 10)
        _(usd[:rate]).must_be(:<, 20)
      end

      it "rounds workbook mids to four decimals" do
        dataset = adapter.fetch(after: Date.new(2026, 8, 3), upto: Date.new(2026, 8, 7))

        dataset.each { |r| _(r[:rate]).must_equal(r[:rate].round(4)) }
      end

      it "reaches back to the start of the workbook" do
        dataset = adapter.fetch(after: Date.new(2000, 1, 4), upto: Date.new(2000, 1, 4))
        usd = dataset.find { |r| r[:base] == "USD" }

        _(dataset.size).must_equal(3)
        _(usd[:rate]).must_equal(5.3441)
      end

      it "respects the after and upto bounds" do
        dataset = adapter.fetch(after: Date.new(2026, 8, 5), upto: Date.new(2026, 8, 6))
        dates = dataset.map { |r| r[:date] }.uniq.sort

        _(dates).must_equal([Date.new(2026, 8, 5), Date.new(2026, 8, 6)])
      end

      it "includes the live day" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 9), upto: Date.new(2026, 9, 9))

        _(dataset.map { |r| r[:base] }.sort).must_equal(["EUR", "GBP", "USD"])
      end

      describe "#parse_live" do
        it "parses the CAR mids" do
          json = <<~JSON
            {"car":[{"usdmid":"14.4077","currentDate":"09-Sep-2026","usdbuy":"14.2268","eursell":"17.3166",
            "gbpdiff":"0.7936","gbpbuy":"19.1057","usddiff":"-0.2089","gbpmid":"20.1872","usdsell":"14.7228",
            "eurdiff":"0.0284","eurmid":"16.8958","id":1,"eurbuy":"16.8603","gbpsell":"20.3083"}]}
          JSON

          records = adapter.parse_live(json)

          _(records.size).must_equal(3)
          _(records.first).must_equal({ date: Date.new(2026, 9, 9), base: "USD", quote: "SCR", rate: 14.4077 })
          _(records.last[:rate]).must_equal(20.1872)
        end

        it "raises when the CAR block is missing" do
          assert_raises(RuntimeError) { adapter.parse_live('{"car":[]}') }
        end
      end
    end
  end
end
