# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/cbar"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe CBAR do
      before do
        VCR.insert_cassette("cbar", match_requests_on: [:method, :uri])
      end

      after { VCR.eject_cassette }

      let(:adapter) { CBAR.new }

      def valute(code, nominal, value)
        %(<Valute Code="#{code}"><Nominal>#{nominal}</Nominal><Name>#{code}</Name><Value>#{value}</Value></Valute>)
      end

      def bulletin(date, body)
        <<~XML
          <?xml version="1.0" encoding="UTF-8"?>
          <ValCurs Date="#{date}" Name="AZN məzənnələri">
            <ValType Type="Bank metalları">
              #{valute("XAU", "1 t.u.", "7541.608")}
              #{valute("XPD", "1 t.u.", "")}
            </ValType>
            <ValType Type="Xarici valyutalar">
              #{body}
            </ValType>
          </ValCurs>
        XML
      end

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 4), upto: Date.new(2026, 9, 8))

        _(dataset).wont_be_empty
        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["AZN"])
      end

      it "dedupes weekend files on the bulletin date" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 4), upto: Date.new(2026, 9, 8))
        dates = dataset.map { |r| r[:date] }.uniq

        _(dates).must_equal([Date.new(2026, 9, 4), Date.new(2026, 9, 7), Date.new(2026, 9, 8)])
      end

      it "fetches currencies and metals per date" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 8), upto: Date.new(2026, 9, 8))
        bases = dataset.map { |r| r[:base] }

        _(bases.size).must_be(:>, 30)
        _(bases).must_include("USD")
        _(bases).must_include("XAU")
        _(bases).must_include("XDR")
      end

      it "parses currencies with correct base and quote" do
        xml = bulletin("08.09.2026", valute("USD", 1, "1.7"))
        records = adapter.parse(xml)
        usd = records.find { |r| r[:base] == "USD" }

        _(usd[:date]).must_equal(Date.new(2026, 9, 8))
        _(usd[:quote]).must_equal("AZN")
        _(usd[:rate]).must_equal(1.7)
      end

      it "normalizes rate by nominal" do
        xml = bulletin("08.09.2026", valute("JPY", 100, "1.0608"))
        records = adapter.parse(xml)

        _(records.find { |r| r[:base] == "JPY" }[:rate]).must_be_close_to(0.010608, 1e-9)
      end

      it "quotes metals per troy ounce and skips empty values" do
        records = adapter.parse(bulletin("08.09.2026", ""))

        _(records.map { |r| r[:base] }).must_equal(["XAU"])
        _(records.first[:rate]).must_equal(7541.608)
      end

      it "maps SDR to XDR" do
        xml = bulletin("08.09.2026", valute("SDR", 1, "2.3335"))
        records = adapter.parse(xml)

        _(records.map { |r| r[:base] }).must_include("XDR")
      end

      it "stores pre-2006 bulletins as old manat" do
        xml = bulletin("29.12.2005", valute("USD", 1, "4593"))
        records = adapter.parse(xml)

        _(records.map { |r| r[:quote] }.uniq).must_equal(["AZM"])
      end

      it "restores predecessor codes before a redenomination" do
        rows = <<~XML
          #{valute("RUB", 1, "0.65")}
          #{valute("TRY", 1, "19")}
          #{valute("USD", 1, "3888")}
        XML
        records = adapter.parse(bulletin("30.12.1997", rows))
        by_base = records.to_h { |r| [r[:base], r[:rate]] }

        _(by_base.keys).must_equal(["XAU", "RUR", "TRL", "USD"])
        _(by_base["RUR"]).must_equal(0.65)
        _(by_base["TRL"]).must_equal(0.019)
      end

      it "keeps current codes from the cutover date" do
        rows = <<~XML
          #{valute("BYN", 1, "0.7674")}
          #{valute("TRY", 1, "0.5352")}
        XML
        records = adapter.parse(bulletin("01.07.2016", rows))

        _(records.map { |r| r[:base] }).must_equal(["XAU", "BYN", "TRY"])
        _(records.map { |r| r[:quote] }.uniq).must_equal(["AZN"])
      end

      it "raises on an unexpected document" do
        _ { adapter.parse("<html><body>moved</body></html>") }.must_raise(RuntimeError)
      end
    end
  end
end
