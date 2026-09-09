# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/boz"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BOZ do
      before do
        VCR.insert_cassette("boz", match_requests_on: [:method, :host, :path])
      end

      after do
        VCR.eject_cassette
      end

      let(:adapter) { BOZ.new }

      it "fetches rates from the workbook" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 1), upto: Date.new(2026, 9, 8))

        _(dataset).wont_be_empty
      end

      it "emits ZMW as the quote currency" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 1), upto: Date.new(2026, 9, 8))

        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["ZMW"])
      end

      it "covers the four currencies BoZ publishes" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 1), upto: Date.new(2026, 9, 8))

        _(dataset.map { |r| r[:base] }.uniq.sort).must_equal(["EUR", "GBP", "USD", "ZAR"])
      end

      it "respects the after and upto bounds" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 3), upto: Date.new(2026, 9, 4))
        dates = dataset.map { |r| r[:date] }.uniq.sort

        _(dates).must_equal([Date.new(2026, 9, 3), Date.new(2026, 9, 4)])
      end

      it "returns the buy/sell midpoint for USD/ZMW" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 8), upto: Date.new(2026, 9, 8))
        usd = dataset.find { |r| r[:base] == "USD" }

        _(usd[:rate]).must_be_within_epsilon(19.2291, 1e-6)
      end

      it "labels rows before the 2013 rebasing as old kwacha" do
        dataset = adapter.fetch(after: Date.new(2012, 12, 28), upto: Date.new(2013, 1, 2))
        by_date = dataset.select { |r| r[:base] == "USD" }.to_h { |r| [r[:date], r] }

        _(by_date[Date.new(2012, 12, 28)][:quote]).must_equal("ZMK")
        _(by_date[Date.new(2012, 12, 28)][:rate]).must_be(:>, 5000)
        _(by_date[Date.new(2013, 1, 2)][:quote]).must_equal("ZMW")
        _(by_date[Date.new(2013, 1, 2)][:rate]).must_be(:<, 6)
      end

      it "skips the hidden placeholder rows at the top of the sheet" do
        dataset = adapter.fetch(after: Date.new(2006, 1, 1), upto: Date.new(2006, 1, 12))

        _(dataset.map { |r| r[:date] }.uniq).must_equal([Date.new(2006, 1, 12)])
      end

      describe "#workbook_url" do
        it "resolves the attached file of the first node" do
          json = {
            data: [{ id: "n1", relationships: { field_average_historical_file: { data: { id: "f1" } } } }],
            included: [
              { id: "f1", attributes: { uri: { url: "/sites/default/files/2026-09/AVERAGE_FXRATES_3.xlsx" } } },
            ],
          }.to_json

          _(adapter.workbook_url(json))
            .must_equal("https://www.boz.zm/sites/default/files/2026-09/AVERAGE_FXRATES_3.xlsx")
        end

        it "raises when the listing is empty" do
          assert_raises(RuntimeError) { adapter.workbook_url({ data: [] }.to_json) }
        end

        it "raises when the node has no file attached" do
          json = { data: [{ id: "n1", relationships: {} }], included: [] }.to_json

          assert_raises(RuntimeError) { adapter.workbook_url(json) }
        end
      end
    end
  end
end
