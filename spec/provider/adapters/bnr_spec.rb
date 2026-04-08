# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bnr"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BNR do
      before do
        VCR.insert_cassette("bnr", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { BNR.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 1), upto: Date.new(2026, 4, 5))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 1), upto: Date.new(2026, 4, 5))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "parses XML correctly" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <DataSet xmlns="http://www.bnr.ro/xsd">
            <Header><Publisher>National Bank of Romania</Publisher></Header>
            <Body>
              <Cube date="2026-04-03">
                <Rate currency="EUR">5.0978</Rate>
                <Rate currency="USD">4.4169</Rate>
                <Rate currency="HUF" multiplier="100">1.3229</Rate>
              </Cube>
            </Body>
          </DataSet>
        XML

        records = adapter.parse(xml)

        _(records.length).must_equal(3)

        eur = records.find { |r| r[:base] == "EUR" }

        _(eur[:quote]).must_equal("RON")
        _(eur[:rate]).must_equal(5.0978)
        _(eur[:date]).must_equal(Date.new(2026, 4, 3))

        usd = records.find { |r| r[:base] == "USD" }

        _(usd[:rate]).must_equal(4.4169)

        huf = records.find { |r| r[:base] == "HUF" }

        _(huf[:rate]).must_be_close_to(0.013229, 0.000001)
      end

      it "filters by date range" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 1), upto: Date.new(2026, 4, 3))

        dates = dataset.map { |r| r[:date] }.uniq.sort

        _(dates).must_include(Date.new(2026, 4, 2))
        _(dates).must_include(Date.new(2026, 4, 3))
        dates.each { |d| _(d).must_be(:>, Date.new(2026, 4, 1)) }
      end
    end
  end
end
