# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/sama"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe SAMA do
      before do
        VCR.insert_cassette("sama", match_requests_on: [:method, :uri])
      end

      after { VCR.eject_cassette }

      let(:adapter) { SAMA.new }

      def row(code, rate, date = "2026-11-03T00:00:00")
        { "CurrencyCode" => code, "Title" => "", "CurrencyRate" => rate, "CurrencyDate" => date }
      end

      def body(*rows)
        { "itemsCount" => rows.size, "data" => rows }.to_json
      end

      it "fetches every calendar day after a date" do
        dataset = adapter.fetch(after: Date.new(2026, 10, 30), upto: Date.new(2026, 11, 3))
        dates = dataset.map { |r| r[:date] }.uniq.sort

        _(dates).must_equal((Date.new(2026, 10, 31)..Date.new(2026, 11, 3)).to_a)
        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["SAR"])
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 10, 30), upto: Date.new(2026, 11, 3))
        sample = dataset.select { |r| r[:date] == Date.new(2026, 11, 3) }
        usd = sample.find { |r| r[:base] == "USD" }

        _(sample.size).must_be(:>, 70)
        _(sample.map { |r| r[:base] }).must_include("EUR")
        _(usd[:rate]).must_equal(3.75)
      end

      it "repeats Saturday on Sunday and Monday" do
        dataset = adapter.fetch(after: Date.new(2026, 10, 30), upto: Date.new(2026, 11, 3))
        eur = dataset.select { |r| r[:base] == "EUR" }.to_h { |r| [r[:date], r[:rate]] }

        _(eur[Date.new(2026, 11, 1)]).must_equal(eur[Date.new(2026, 10, 31)])
        _(eur[Date.new(2026, 11, 2)]).must_equal(eur[Date.new(2026, 10, 31)])
        _(eur[Date.new(2026, 11, 3)]).wont_equal(eur[Date.new(2026, 11, 2)])
      end

      it "starts at the first day of the archive" do
        VCR.use_cassette("sama_coverage_start", match_requests_on: [:method, :uri], exclusive: true) do
          dataset = adapter.fetch(after: Date.new(2021, 4, 8), upto: Date.new(2021, 4, 10))

          _(dataset.map { |r| r[:date] }.uniq).must_equal([Date.new(2021, 4, 10)])
        end
      end

      it "parses SAR per unit with foreign base" do
        records = adapter.parse(body(row("EUR=", 4.19887)))

        _(records).must_equal([{ date: Date.new(2026, 11, 3), base: "EUR", quote: "SAR", rate: 4.19887 }])
      end

      it "maps SDR to XDR" do
        records = adapter.parse(body(row("SDR=", 5.07705)))

        _(records.first[:base]).must_equal("XDR")
      end

      it "skips the pivot and currencies retired before the archive" do
        records = adapter.parse(body(row("SAR=", 1.0), row("CYP=", 7.14149), row("MRO=", 0.01048), row("USD=", 3.75)))

        _(records.map { |r| r[:base] }).must_equal(["USD"])
      end

      it "returns nothing for a day without a table" do
        _(adapter.parse(body)).must_be_empty
      end
    end
  end
end
