# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/cbic"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe CBIC do
      before do
        VCR.insert_cassette("cbic", match_requests_on: [:method, :uri, :body])
      end

      after { VCR.eject_cassette }

      let(:adapter) { CBIC.new }

      it "fetches import and export rates in force within a window" do
        dataset = adapter.fetch(after: Date.new(2026, 10, 1), upto: Date.new(2026, 10, 20))

        _(dataset.map { |r| r[:date] }.uniq.sort).must_equal([Date.new(2026, 10, 2), Date.new(2026, 10, 16)])
        _(dataset.map { |r| r[:dataset] }.uniq.sort).must_equal(["CBIC.EXPORT", "CBIC.IMPORT"])
        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["INR"])
      end

      it "dates rates on the day after publication" do
        dataset = adapter.fetch(after: Date.new(2026, 10, 15), upto: Date.new(2026, 10, 16))
        usd = dataset.select { |r| r[:base] == "USD" }.to_h { |r| [r[:dataset], r[:rate]] }

        _(dataset.map { |r| r[:date] }.uniq).must_equal([Date.new(2026, 10, 16)])
        _(usd).must_equal("CBIC.IMPORT" => 95.9, "CBIC.EXPORT" => 94.2)
      end

      it "reads after as exclusive" do
        dataset = adapter.fetch(after: Date.new(2026, 10, 2), upto: Date.new(2026, 10, 20))

        _(dataset.map { |r| r[:date] }.uniq).must_equal([Date.new(2026, 10, 16)])
      end

      describe "#parse" do
        def notification(*rows)
          { notificationNumber: "29/2026", status: 1, notPublishDate: "16-10-2026", error: nil, currencyDetail: rows }
            .to_json
        end

        it "quotes INR per foreign unit for each dataset" do
          usd = { currencyCode: "USD", cbicImport: 95.9, cbicExport: 94.2, units: "1.0" }
          records = adapter.parse(notification(usd))

          _(records).must_equal([
            { dataset: "CBIC.IMPORT", date: Date.new(2026, 10, 16), base: "USD", quote: "INR", rate: 95.9 },
            { dataset: "CBIC.EXPORT", date: Date.new(2026, 10, 16), base: "USD", quote: "INR", rate: 94.2 },
          ])
        end

        it "divides per-100 rates by their units" do
          jpy = { currencyCode: "JPY", cbicImport: 61.2, cbicExport: 59.35, units: "100.0" }
          records = adapter.parse(notification(jpy))

          _(records.map { |r| r[:rate] }).must_equal([0.612, 0.5935])
        end

        it "skips rows without units" do
          krw = { currencyCode: "KRW", cbicImport: 7.25, cbicExport: 6.85, units: "0" }
          eur = { currencyCode: "EUR", cbicImport: 108.55, cbicExport: 104.85, units: "1.0" }
          records = adapter.parse(notification(krw, eur))

          _(records.map { |r| r[:base] }.uniq).must_equal(["EUR"])
        end

        it "raises on an error response" do
          json = { notificationNumber: "29/2026", status: 0, error: "not found" }.to_json

          _ { adapter.parse(json) }.must_raise(RuntimeError)
        end
      end
    end
  end
end
