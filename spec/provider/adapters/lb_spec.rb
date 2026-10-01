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

      it "restores the old manat code before the 2006 redenomination" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <FxRates xmlns="http://www.lb.lt/WebServices/FxRates">
            <FxRate>
              <Tp>LT</Tp>
              <Dt>2005-12-30</Dt>
              <CcyAmt><Ccy>LTL</Ccy><Amt>0.63014</Amt></CcyAmt>
              <CcyAmt><Ccy>AZN</Ccy><Amt>1000</Amt></CcyAmt>
            </FxRate>
            <FxRate>
              <Tp>LT</Tp>
              <Dt>2006-01-09</Dt>
              <CcyAmt><Ccy>LTL</Ccy><Amt>3.1077</Amt></CcyAmt>
              <CcyAmt><Ccy>AZN</Ccy><Amt>1</Amt></CcyAmt>
            </FxRate>
          </FxRates>
        XML

        records = adapter.parse(xml)

        _(records.map { |r| r[:base] }).must_equal(["AZM", "AZN"])
        _(records.first[:rate]).must_be_close_to(0.00063014, 1e-9)
      end

      it "restores the old Turkmen manat code before the 2009 redenomination" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <FxRates xmlns="http://www.lb.lt/WebServices/FxRates">
            <FxRate>
              <Tp>LT</Tp>
              <Dt>2008-12-31</Dt>
              <CcyAmt><Ccy>LTL</Ccy><Amt>1.7354</Amt></CcyAmt>
              <CcyAmt><Ccy>TMT</Ccy><Amt>10000</Amt></CcyAmt>
            </FxRate>
            <FxRate>
              <Tp>LT</Tp>
              <Dt>2009-01-01</Dt>
              <CcyAmt><Ccy>LTL</Ccy><Amt>8.6770</Amt></CcyAmt>
              <CcyAmt><Ccy>TMT</Ccy><Amt>10</Amt></CcyAmt>
            </FxRate>
          </FxRates>
        XML

        records = adapter.parse(xml)

        _(records.map { |r| r[:base] }).must_equal(["TMM", "TMT"])
        _(records.first[:rate]).must_be_close_to(0.00017354, 1e-9)
      end

      it "restores the 1994 Belarusian ruble code before the 2000 redenomination" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <FxRates xmlns="http://www.lb.lt/WebServices/FxRates">
            <FxRate>
              <Tp>LT</Tp>
              <Dt>1999-12-31</Dt>
              <CcyAmt><Ccy>LTL</Ccy><Amt>4.444</Amt></CcyAmt>
              <CcyAmt><Ccy>BYR</Ccy><Amt>1000000</Amt></CcyAmt>
            </FxRate>
            <FxRate>
              <Tp>LT</Tp>
              <Dt>2000-01-03</Dt>
              <CcyAmt><Ccy>LTL</Ccy><Amt>4.4444</Amt></CcyAmt>
              <CcyAmt><Ccy>BYR</Ccy><Amt>1000</Amt></CcyAmt>
            </FxRate>
          </FxRates>
        XML

        records = adapter.parse(xml)

        _(records.map { |r| r[:base] }).must_equal(["BYB", "BYR"])
        _(records.first[:rate]).must_be_close_to(0.000004444, 1e-12)
      end

      it "restores predecessor codes until LB's values switch to the redenominated unit" do
        quotes = [
          ["1995-01-02", "PLN", 1000, 0.1641], ["1995-01-03", "PLN", 1, 1.646],
          ["1998-01-02", "RUB", 1000, 0.6694], ["1998-01-05", "RUB", 1, 0.6672],
          ["1999-07-06", "BGN", 1000, 2.1467], ["1999-07-07", "BGN", 1, 2.0952],
          ["2005-07-01", "RON", 100_000, 9.5538], ["2005-07-04", "RON", 10, 9.5814],
          ["2006-07-07", "MZN", 10_000, 1.0536], ["2006-07-10", "MZN", 10, 1.0531],
        ]
        rates = quotes.map do |date, code, amount, ltl|
          "<FxRate><Tp>LT</Tp><Dt>#{date}</Dt><CcyAmt><Ccy>LTL</Ccy><Amt>#{ltl}</Amt></CcyAmt>" \
            "<CcyAmt><Ccy>#{code}</Ccy><Amt>#{amount}</Amt></CcyAmt></FxRate>"
        end
        xml = %(<?xml version="1.0" encoding="utf-8"?><FxRates xmlns="http://www.lb.lt/WebServices/FxRates">#{rates.join}</FxRates>)

        records = adapter.parse(xml)
        bases = ["PLZ", "PLN", "RUR", "RUB", "BGL", "BGN", "ROL", "RON", "MZM", "MZN"]

        _(records.map { |r| r[:base] }).must_equal(bases)
        _(records.first[:rate]).must_be_close_to(0.0001641, 1e-10)
        _(records[1][:rate]).must_be_close_to(1.646, 1e-9)
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

    describe "LB archive" do
      before do
        VCR.insert_cassette("lb_archive", match_requests_on: [:method, :uri])
      end

      after { VCR.eject_cassette }

      let(:adapter) { LB.new }

      def rates(date)
        adapter.fetch(after: date, upto: date).to_h { |r| [r[:base], r[:rate]] }
      end

      it "fetches the first bulletin, with the karbovanets and the old ruble and zloty" do
        first = rates(Date.new(1993, 6, 25))

        _(first["UAK"]).must_be_close_to(0.001131, 1e-9)
        _(first["RUR"]).must_be_close_to(0.004128, 1e-9)
        _(first["PLZ"]).must_be_close_to(0.000262, 1e-9)
        _(first.keys & ["RUB", "PLN"]).must_be_empty
      end

      it "skips rows that repeat the Russian ruble under other countries' codes" do
        copies = rates(Date.new(1995, 7, 10))
        tajik = rates(Date.new(1995, 7, 11))
        before = rates(Date.new(1995, 11, 29))
        turkmen = rates(Date.new(1995, 11, 30))

        _(copies["RUR"]).must_be_close_to(0.000873, 1e-9)
        _(copies.keys & ["GER", "TJR", "TMM"]).must_be_empty
        _(tajik["TJR"]).must_be_close_to(0.074074, 1e-9)
        _(tajik.keys & ["GER", "TMM"]).must_be_empty
        _(before.keys).wont_include("TMM")
        _(turkmen["TMM"]).must_be_close_to(0.002759, 1e-9)
      end

      it "reads the Belarusian ruble before the 1994 denomination per 10" do
        _(rates(Date.new(1994, 8, 19))["BYB"]).must_be_close_to(0.0014, 1e-9)
        _(rates(Date.new(1994, 8, 22))["BYB"]).must_be_close_to(0.001421, 1e-9)
      end

      it "relabels the dinar LB quotes as YUN" do
        yun = rates(Date.new(1998, 7, 6))
        yum = rates(Date.new(1998, 7, 7))

        _(yun.keys).wont_include("YUN")
        _(yun["YUM"]).must_be_close_to(0.3734, 1e-9)
        _(yum["YUM"]).must_be_close_to(0.3712, 1e-9)
      end
    end
  end
end
