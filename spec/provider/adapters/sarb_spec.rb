# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/sarb"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe SARB do
      before do
        VCR.insert_cassette("sarb", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { SARB.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 23), upto: Date.new(2026, 3, 27))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 23), upto: Date.new(2026, 3, 27))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "parses JSON with correct base and quote" do
        json = '[{"Period":"2026-03-24T00:00:00","Value":18.2345}]'

        records = adapter.parse(json, base: "USD", quote: "ZAR")

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("ZAR")
        _(records.first[:rate]).must_equal(18.2345)
        _(records.first[:date]).must_equal(Date.new(2026, 3, 24))
      end

      it "skips zero values" do
        json = '[{"Period":"2026-03-24T00:00:00","Value":0}]'

        records = adapter.parse(json, base: "ZAR", quote: "AUD")

        _(records).must_be_empty
      end

      it "skips null values" do
        json = '[{"Period":"2026-03-24T00:00:00","Value":null}]'

        records = adapter.parse(json, base: "ZAR", quote: "AUD")

        _(records).must_be_empty
      end
    end
  end
end
