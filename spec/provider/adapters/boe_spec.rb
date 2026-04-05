# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/boe"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BOE do
      before do
        VCR.insert_cassette("boe", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { BOE.new }

      it "fetches rates with date range" do
        start = Date.new(2026, 3, 17)
        upto = Date.new(2026, 3, 20)

        dataset = adapter.fetch(after: start, upto:)

        dates = dataset.map { |r| r[:date] }.uniq.sort

        _(dates.size).must_be(:>=, 1)
        _(dates.min).must_be(:>=, start)
        _(dates.max).must_be(:<=, upto)
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 17), upto: Date.new(2026, 3, 20))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "parses CSV with correct base and quote" do
        csv = <<~CSV
          DATE,XUDLUSS,XUDLERS
          17 Mar 2026,1.3343,1.1577
        CSV

        records = adapter.parse(csv)

        usd = records.find { |r| r[:quote] == "USD" }
        eur = records.find { |r| r[:quote] == "EUR" }

        _(usd[:base]).must_equal("GBP")
        _(usd[:rate]).must_equal(1.3343)
        _(eur[:base]).must_equal("GBP")
        _(eur[:rate]).must_equal(1.1577)
        _(usd[:date]).must_equal(Date.new(2026, 3, 17))
      end

      it "skips empty values" do
        csv = <<~CSV
          DATE,XUDLUSS,XUDLERS
          17 Mar 2026,1.3343,
        CSV

        records = adapter.parse(csv)

        _(records.length).must_equal(1)
        _(records.first[:quote]).must_equal("USD")
      end

      it "skips zero rates" do
        csv = <<~CSV
          DATE,XUDLUSS
          17 Mar 2026,0
        CSV

        records = adapter.parse(csv)

        _(records).must_be_empty
      end
    end
  end
end
