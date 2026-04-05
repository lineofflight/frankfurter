# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/imf"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe IMF do
      before do
        VCR.insert_cassette("imf", match_requests_on: [:method, :host])
      end

      after do
        VCR.eject_cassette
      end

      let(:adapter) { IMF.new }

      it "fetches rates" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 31))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 31))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
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
    end
  end
end
