# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/mnb"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe MNB do
      before do
        VCR.insert_cassette("mnb", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { MNB.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2025, 3, 24), upto: Date.new(2025, 3, 28))

        dates = dataset.map { |r| r[:date] }.uniq

        _(dates.length).must_be(:>=, 3)
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2025, 3, 24), upto: Date.new(2025, 3, 28))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "fetches foreign currency as base and HUF as quote" do
        dataset = adapter.fetch(after: Date.new(2025, 3, 24), upto: Date.new(2025, 3, 28))
        eur = dataset.find { |r| r[:base] == "EUR" && r[:quote] == "HUF" }

        _(eur).wont_be_nil
        _(eur[:rate]).must_be(:>, 300)
      end

      it "parses XML with unit multiplier" do
        xml = <<~XML
          <MNBExchangeRates>
            <Day date="2025-03-24">
              <Rate unit="100" curr="JPY">253,40</Rate>
            </Day>
          </MNBExchangeRates>
        XML

        records = adapter.parse(xml)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("JPY")
        _(records.first[:quote]).must_equal("HUF")
        _(records.first[:rate]).must_be_close_to(2.534, 0.001)
        _(records.first[:date]).must_equal(Date.new(2025, 3, 24))
      end

      it "parses XML with unit 1" do
        xml = <<~XML
          <MNBExchangeRates>
            <Day date="2025-03-24">
              <Rate unit="1" curr="EUR">398,44</Rate>
            </Day>
          </MNBExchangeRates>
        XML

        records = adapter.parse(xml)

        _(records.first[:rate]).must_be_close_to(398.44, 0.01)
      end

      it "skips records with zero rate" do
        xml = <<~XML
          <MNBExchangeRates>
            <Day date="2025-03-24">
              <Rate unit="1" curr="EUR">0,00</Rate>
            </Day>
          </MNBExchangeRates>
        XML

        records = adapter.parse(xml)

        _(records).must_be_empty
      end
    end
  end
end
