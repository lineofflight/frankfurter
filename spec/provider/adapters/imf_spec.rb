# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/imf"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe IMF do
      before do
        VCR.insert_cassette("imf", match_requests_on: [:method, :uri])
      end

      after do
        VCR.eject_cassette
      end

      let(:adapter) { IMF.new }

      it "fetches rates across both Currency blocks in the TSV response" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 31))
        dates = dataset.map { |r| r[:date] }.uniq

        _(dates).must_include(Date.new(2026, 3, 17))
        _(dates.size).must_be(:>, 11)
      end

      it "fetches multiple currencies per date without duplicating records" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 31))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)

        keys = dataset.map { |r| [r[:date], r[:base], r[:quote]] }

        _(keys.size).must_equal(keys.uniq.size)
      end

      it "parses indirect quotes with (1) suffix" do
        tsv = <<~TSV
          Representative Exchange Rates for Selected Currencies for January 2026
          Currency\tJanuary 02, 2026\tJanuary 05, 2026
          Euro(1)\t1.1698\t1.1606
          Japanese yen\t156.40\t157.41
          U.S. dollar\t1.0000\t1.0000
        TSV

        records = adapter.parse(tsv)

        eur = records.select { |r| r[:base] == "EUR" || r[:quote] == "EUR" }

        _(eur.first[:base]).must_equal("EUR")
        _(eur.first[:quote]).must_equal("USD")

        jpy = records.select { |r| r[:base] == "USD" && r[:quote] == "JPY" }

        _(jpy.first[:rate]).must_equal(156.4)
      end

      it "parses KWD as direct quote (units per USD)" do
        tsv = <<~TSV
          Representative Exchange Rates for Selected Currencies for March 2026
          Currency\tMarch 02, 2026
          Kuwaiti dinar\t0.306600
        TSV

        records = adapter.parse(tsv)
        kwd = records.find { |r| r[:quote] == "KWD" || r[:base] == "KWD" }

        _(kwd[:base]).must_equal("USD")
        _(kwd[:quote]).must_equal("KWD")
        _(kwd[:rate]).must_equal(0.3066)
      end

      it "skips USD rows" do
        tsv = <<~TSV
          Representative Exchange Rates for Selected Currencies for January 2026
          Currency\tJanuary 02, 2026
          U.S. dollar\t1.0000
          Euro(1)\t1.1698
        TSV

        records = adapter.parse(tsv)

        _(records.none? { |r| r[:base] == "USD" && r[:quote] == "USD" }).must_equal(true)
      end

      it "parses Continued blocks with their own date header" do
        tsv = <<~TSV
          Representative Exchange Rates for Selected Currencies for March 2026
          Currency\tMarch 02, 2026\tMarch 03, 2026
          Chinese yuan\t6.882900\t6.897100
          U.S. dollar\t1.000000\t1.000000

          Representative Exchange Rates for Selected Currencies for March 2026 Continued

          Currency\tMarch 17, 2026\tMarch 18, 2026
          Chinese yuan\t6.888300\t6.875600
          U.S. dollar\t1.000000\t1.000000
        TSV

        records = adapter.parse(tsv)
        cny = records.select { |r| r[:quote] == "CNY" }.sort_by { |r| r[:date] }

        _(cny.map { |r| r[:date] }).must_equal([
          Date.new(2026, 3, 2),
          Date.new(2026, 3, 3),
          Date.new(2026, 3, 17),
          Date.new(2026, 3, 18),
        ])
        _(cny.find { |r| r[:date] == Date.new(2026, 3, 17) }[:rate]).must_equal(6.8883)
      end

      it "does not emit duplicate (date, base, quote) records across blocks" do
        tsv = <<~TSV
          Currency\tMarch 02, 2026\tMarch 03, 2026
          Chinese yuan\t6.882900\t6.897100

          Currency\tMarch 17, 2026\tMarch 18, 2026
          Chinese yuan\t6.888300\t6.875600
        TSV

        records = adapter.parse(tsv)
        keys = records.map { |r| [r[:date], r[:base], r[:quote]] }

        _(keys.size).must_equal(keys.uniq.size)
      end

      it "fetches SDR cross rates alongside representative rates" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 31))
        sdr = dataset.select { |r| r[:quote] == "XDR" }

        _(sdr).wont_be_empty
        _(sdr.map { |r| r[:base] }.uniq).must_include("EUR")
        _(sdr.map { |r| r[:base] }.uniq).must_include("USD")
      end

      it "parses SDR cross rates as base=currency, quote=XDR" do
        tsv = <<~TSV
          SDRs per Currency unit for April 2026
          Currency\tApril 01, 2026\tApril 02, 2026
          Euro\t0.8507900000\t0.8483160000
          Japanese yen\t0.0046154900\t0.0046392700
          U.S. dollar\t0.7331240000\t0.7360660000
        TSV

        records = adapter.parse_sdrcv(tsv)

        eur = records.find { |r| r[:base] == "EUR" && r[:date] == Date.new(2026, 4, 1) }

        _(eur[:quote]).must_equal("XDR")
        _(eur[:rate]).must_equal(0.85079)

        usd = records.find { |r| r[:base] == "USD" && r[:date] == Date.new(2026, 4, 1) }

        _(usd[:quote]).must_equal("XDR")
        _(usd[:rate]).must_equal(0.7331240)
      end

      it "skips SDR rows with NA values" do
        tsv = <<~TSV
          SDRs per Currency unit for April 2026
          Currency\tApril 01, 2026\tApril 02, 2026
          Euro\tNA\t0.8483160000
        TSV

        records = adapter.parse_sdrcv(tsv)

        _(records.size).must_equal(1)
        _(records.first[:date]).must_equal(Date.new(2026, 4, 2))
      end
    end
  end
end
