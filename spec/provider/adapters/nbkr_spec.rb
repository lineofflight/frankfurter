# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/nbkr"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe NBKR do
      let(:adapter) { NBKR.new }

      describe "live XML feed" do
        before do
          VCR.insert_cassette("nbkr_live", match_requests_on: [:method, :uri], allow_playback_repeats: true)
        end

        after { VCR.eject_cassette }

        it "fetches rates from both daily and weekly endpoints when upto is unset" do
          dataset = adapter.fetch

          _(dataset).wont_be_empty
        end

        it "fetches foreign currency as base and KGS as quote" do
          dataset = adapter.fetch
          usd = dataset.find { |r| r[:base] == "USD" }

          _(usd).wont_be_nil
          _(usd[:quote]).must_equal("KGS")
          _(usd[:rate]).must_be(:>, 50)
        end

        it "includes both daily majors and weekly currencies" do
          dataset = adapter.fetch
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

        it "filters live records by after" do
          _(adapter.fetch(after: Date.new(2026, 5, 25))).must_be_empty
        end
      end

      describe "historical HTML scrape" do
        before do
          VCR.insert_cassette("nbkr_historical", match_requests_on: [:method, :uri], allow_playback_repeats: true)
        end

        after { VCR.eject_cassette }

        # All HTTP-hitting historical tests share one window so the cassette
        # records the per-currency requests just once.
        let(:after) { Date.new(2005, 6, 1) }
        let(:upto) { Date.new(2005, 6, 30) }

        it "fetches per-currency time series for a past window" do
          dataset = adapter.fetch(after:, upto:)

          _(dataset).wont_be_empty
          usd = dataset.find { |r| r[:base] == "USD" }

          _(usd).wont_be_nil
          _(usd[:quote]).must_equal("KGS")
          # USD/KGS in mid-2000s was roughly 40-45.
          _(usd[:rate]).must_be_close_to(41.0, 5.0)
        end

        it "constrains records to the requested window" do
          dataset = adapter.fetch(after:, upto:)

          _(dataset.map { |r| r[:date] }.min).must_be(:>=, after)
          _(dataset.map { |r| r[:date] }.max).must_be(:<=, upto)
        end
      end

      describe "#parse" do
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
      end

      describe "#parse_historical" do
        it "extracts (date, value) pairs from the per-currency HTML series" do
          html = <<~HTML
            <table><tr>
              <td><!--date-->04.06.2005<!--date--></td>
              <td><!--value-->40,9879<!--value--></td>
            </tr><tr>
              <td><!--date-->28.05.2005<!--date--></td>
              <td><!--value-->40,9577<!--value--></td>
            </tr></table>
          HTML

          records = adapter.parse_historical(html, iso: "USD", nominal: 1)

          _(records.size).must_equal(2)
          _(records.first[:date]).must_equal(Date.new(2005, 6, 4))
          _(records.first[:base]).must_equal("USD")
          _(records.first[:quote]).must_equal("KGS")
          _(records.first[:rate]).must_be_close_to(40.9879, 0.0001)
        end

        it "normalizes by nominal for currencies quoted per 10 or 100 units" do
          html = <<~HTML
            <tr><td><!--date-->15.01.1999<!--date--></td><td><!--value-->2,5816<!--value--></td></tr>
            <tr><td><!--date-->01.01.1999<!--date--></td><td><!--value-->0,3402<!--value--></td></tr>
          HTML

          jpy = adapter.parse_historical(html, iso: "JPY", nominal: 10)
          byr = adapter.parse_historical(html, iso: "BYR", nominal: 100)

          _(jpy.first[:rate]).must_be_close_to(0.25816, 0.00001)
          _(byr.last[:rate]).must_be_close_to(0.003402, 0.000001)
        end

        it "skips zero or negative values" do
          html = <<~HTML
            <tr><td><!--date-->01.01.1999<!--date--></td><td><!--value-->0,0000<!--value--></td></tr>
          HTML

          _(adapter.parse_historical(html, iso: "USD", nominal: 1)).must_be_empty
        end
      end
    end
  end
end
