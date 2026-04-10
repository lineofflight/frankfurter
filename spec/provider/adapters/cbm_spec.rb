# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/cbm"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe CBM do
      before do
        VCR.insert_cassette("cbm", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { CBM.new }

      it "fetches rates" do
        dataset = adapter.fetch

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies" do
        dataset = adapter.fetch
        currencies = dataset.map { |r| r[:base] }.uniq

        _(currencies.size).must_be(:>, 10)
      end

      it "parses JSON with correct base and quote" do
        json = '{"timestamp":"1775721600","rates":{"USD":"2100.00","EUR":"2449.65"}}'

        records = adapter.parse(json)

        _(records.length).must_equal(2)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("MMK")
        _(records.first[:rate]).must_equal(2100.0)
        _(records.first[:date]).must_equal(Date.new(2026, 4, 9))
      end

      it "skips zero rates" do
        json = '{"timestamp":"1775721600","rates":{"USD":"2100.00","XYZ":"0.00"}}'

        records = adapter.parse(json)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
      end

      it "returns empty array when data is missing" do
        records = adapter.parse('{"info":"test"}')

        _(records).must_be_empty
      end
    end
  end
end
