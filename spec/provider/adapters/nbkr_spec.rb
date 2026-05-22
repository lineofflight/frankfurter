# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/nbkr"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe NBKR do
      before do
        VCR.insert_cassette("nbkr", match_requests_on: [:method, :uri])
      end

      after { VCR.eject_cassette }

      let(:adapter) { NBKR.new }

      it "fetches rates from both daily and weekly endpoints" do
        dataset = adapter.fetch(upto: Date.new(2026, 5, 23))

        _(dataset).wont_be_empty
      end

      it "fetches foreign currency as base and KGS as quote" do
        dataset = adapter.fetch(upto: Date.new(2026, 5, 23))
        usd = dataset.find { |r| r[:base] == "USD" }

        _(usd).wont_be_nil
        _(usd[:quote]).must_equal("KGS")
        _(usd[:rate]).must_be(:>, 50)
      end

      it "includes both daily majors and weekly currencies" do
        dataset = adapter.fetch(upto: Date.new(2026, 5, 23))
        codes = dataset.map { |r| r[:base] }

        # Daily majors
        _(codes).must_include("USD")
        _(codes).must_include("EUR")
        _(codes).must_include("RUB")
        # Weekly currencies
        _(codes).must_include("GBP")
        _(codes).must_include("JPY")
        _(codes).must_include("CHF")
      end

      it "parses daily XML with comma decimal separators" do
        xml = <<~XML
          <?xml version="1.0" encoding="windows-1251" ?>
          <CurrencyRates Name="Daily Exchange Rates" Date="23.05.2026">
            <Currency ISOCode="USD"><Nominal>1</Nominal><Value>87,4500</Value></Currency>
            <Currency ISOCode="EUR"><Nominal>1</Nominal><Value>101,5076</Value></Currency>
          </CurrencyRates>
        XML

        records = adapter.parse(xml)

        _(records.size).must_equal(2)
        usd = records.find { |r| r[:base] == "USD" }

        _(usd[:date]).must_equal(Date.new(2026, 5, 23))
        _(usd[:quote]).must_equal("KGS")
        _(usd[:rate]).must_be_close_to(87.4500, 0.0001)
      end

      it "normalizes by Nominal for currencies quoted per 10 or 100 units" do
        xml = <<~XML
          <?xml version="1.0" encoding="windows-1251" ?>
          <CurrencyRates Name="Weekly Exchange Rates" Date="23.05.2026">
            <Currency ISOCode="JPY"><Nominal>10</Nominal><ValidFor>7</ValidFor><Value>5,4969</Value></Currency>
            <Currency ISOCode="BYR"><Nominal>100</Nominal><ValidFor>7</ValidFor><Value>0,3402</Value></Currency>
          </CurrencyRates>
        XML

        records = adapter.parse(xml)
        jpy = records.find { |r| r[:base] == "JPY" }
        byr = records.find { |r| r[:base] == "BYR" }

        _(jpy[:rate]).must_be_close_to(0.54969, 0.00001)
        _(byr[:rate]).must_be_close_to(0.003402, 0.000001)
      end

      it "skips invalid or empty values" do
        xml = <<~XML
          <?xml version="1.0" encoding="windows-1251" ?>
          <CurrencyRates Name="Daily Exchange Rates" Date="23.05.2026">
            <Currency ISOCode="USD"><Nominal>1</Nominal><Value>0,0000</Value></Currency>
            <Currency ISOCode="EUR"><Nominal>1</Nominal><Value></Value></Currency>
            <Currency ISOCode="XX"><Nominal>1</Nominal><Value>1,2345</Value></Currency>
          </CurrencyRates>
        XML

        _(adapter.parse(xml)).must_be_empty
      end

      it "filters records by after and upto" do
        xml = <<~XML
          <?xml version="1.0" encoding="windows-1251" ?>
          <CurrencyRates Name="Daily Exchange Rates" Date="23.05.2026">
            <Currency ISOCode="USD"><Nominal>1</Nominal><Value>87,4500</Value></Currency>
          </CurrencyRates>
        XML

        records = adapter.parse(xml)

        _(records.first[:date]).must_equal(Date.new(2026, 5, 23))
      end
    end
  end
end
