# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/cbu"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe CBU do
      before do
        VCR.insert_cassette("cbu", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { CBU.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 1), upto: Date.new(2026, 4, 3))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 1), upto: Date.new(2026, 4, 3))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "parses JSON correctly" do
        json = <<~JSON
          [
            {"id":69,"Code":"840","Ccy":"USD","CcyNm_EN":"US Dollar","Nominal":"1","Rate":"12194.21","Diff":"-16.5","Date":"01.04.2026"},
            {"id":21,"Code":"978","Ccy":"EUR","CcyNm_EN":"Euro","Nominal":"1","Rate":"13984.32","Diff":"-51.89","Date":"01.04.2026"}
          ]
        JSON
        records = adapter.parse(json)

        _(records.length).must_equal(2)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("UZS")
        _(records.first[:rate]).must_be_close_to(12194.21, 0.01)
      end

      it "normalizes rate by nominal" do
        json = <<~JSON
          [{"id":1,"Code":"360","Ccy":"IDR","CcyNm_EN":"Indonesian Rupiah","Nominal":"10","Rate":"7.18","Diff":"0","Date":"01.04.2026"}]
        JSON
        records = adapter.parse(json)

        _(records.first[:rate]).must_be_close_to(0.718, 0.001)
      end

      it "skips zero rates" do
        json = <<~JSON
          [{"id":1,"Code":"840","Ccy":"USD","CcyNm_EN":"US Dollar","Nominal":"1","Rate":"0","Diff":"0","Date":"01.04.2026"}]
        JSON
        records = adapter.parse(json)

        _(records).must_be_empty
      end

      it "skips invalid currency codes" do
        json = <<~JSON
          [{"id":1,"Code":"999","Ccy":"XX","CcyNm_EN":"Invalid","Nominal":"1","Rate":"1.5","Diff":"0","Date":"01.04.2026"}]
        JSON
        records = adapter.parse(json)

        _(records).must_be_empty
      end

      it "handles empty response" do
        records = adapter.parse("[]")

        _(records).must_be_empty
      end
    end
  end
end
