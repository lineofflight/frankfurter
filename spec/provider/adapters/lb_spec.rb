# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/lb"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe LB do
      before do
        VCR.insert_cassette("lb", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { LB.new }

      it "fetches pre-EUR rates" do
        dataset = adapter.fetch(after: Date.new(2014, 12, 29), upto: Date.new(2014, 12, 31))

        _(dataset).wont_be_empty
        _(dataset.first[:quote]).must_equal("LTL")
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2014, 12, 29), upto: Date.new(2014, 12, 31))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "parses LT-type XML with correct base and quote" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <FxRates xmlns="http://www.lb.lt/WebServices/FxRates">
            <FxRate>
              <Tp>LT</Tp>
              <Dt>2014-12-30</Dt>
              <CcyAmt>
                <Ccy>LTL</Ccy>
                <Amt>7.6881</Amt>
              </CcyAmt>
              <CcyAmt>
                <Ccy>AED</Ccy>
                <Amt>10</Amt>
              </CcyAmt>
            </FxRate>
          </FxRates>
        XML

        records = adapter.parse(xml)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("AED")
        _(records.first[:quote]).must_equal("LTL")
        _(records.first[:rate]).must_be_close_to(0.76881, 0.00001)
        _(records.first[:date]).must_equal(Date.new(2014, 12, 30))
      end

      it "parses EU-type XML with correct base and quote" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <FxRates xmlns="http://www.lb.lt/WebServices/FxRates">
            <FxRate>
              <Tp>EU</Tp>
              <Dt>2025-03-17</Dt>
              <CcyAmt>
                <Ccy>EUR</Ccy>
                <Amt>1</Amt>
              </CcyAmt>
              <CcyAmt>
                <Ccy>AUD</Ccy>
                <Amt>1.7160</Amt>
              </CcyAmt>
            </FxRate>
          </FxRates>
        XML

        records = adapter.parse(xml)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("EUR")
        _(records.first[:quote]).must_equal("AUD")
        _(records.first[:rate]).must_be_close_to(1.7160, 0.0001)
        _(records.first[:date]).must_equal(Date.new(2025, 3, 17))
      end

      it "normalizes rate by quantity for LT type" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <FxRates xmlns="http://www.lb.lt/WebServices/FxRates">
            <FxRate>
              <Tp>LT</Tp>
              <Dt>2014-12-30</Dt>
              <CcyAmt>
                <Ccy>LTL</Ccy>
                <Amt>4.8611</Amt>
              </CcyAmt>
              <CcyAmt>
                <Ccy>AFN</Ccy>
                <Amt>100</Amt>
              </CcyAmt>
            </FxRate>
          </FxRates>
        XML

        records = adapter.parse(xml)

        _(records.first[:rate]).must_be_close_to(0.048611, 0.000001)
      end

      it "handles empty response" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <FxRates xmlns="http://www.lb.lt/WebServices/FxRates" />
        XML

        records = adapter.parse(xml)

        _(records).must_be_empty
      end
    end
  end
end
