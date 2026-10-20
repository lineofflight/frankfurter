# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/nbs"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe NBS do
      before do
        VCR.insert_cassette("nbs", match_requests_on: [:method, :host, :body])
      end

      after { VCR.eject_cassette }

      let(:adapter) { NBS.new }

      it "fetches rates since a date" do
        dataset = adapter.fetch(after: Date.new(2026, 10, 1), upto: Date.new(2026, 10, 7))
        dates = dataset.map { |r| r[:date] }.uniq.sort

        _(dates.first).must_equal(Date.new(2026, 10, 2))
        _(dates.last).must_equal(Date.new(2026, 10, 7))
        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["RSD"])
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 10, 1), upto: Date.new(2026, 10, 7))
        sample = dataset.select { |r| r[:date] == Date.new(2026, 10, 7) }

        _(sample.map { |r| r[:base] }).must_include("EUR")
        _(sample.size).must_be(:>, 20)
      end

      it "starts at the first day when a window opens before it" do
        # A full backfill opens the day before coverage starts, which the endpoint rejects with HTTP 500.
        VCR.use_cassette("nbs_coverage_start", match_requests_on: [:method, :host, :body], exclusive: true) do
          dataset = adapter.fetch(after: Date.new(2002, 5, 1), upto: Date.new(2002, 5, 17))

          _(dataset.map { |r| r[:date] }.min).must_equal(Date.new(2002, 5, 15))
        end
      end

      it "recovers whole lists from a range that comes back with one currency per day" do
        # The list formed on 2025-01-03 also applied on 2025-01-06.
        VCR.use_cassette("nbs_collapsed", match_requests_on: [:method, :host, :body], exclusive: true) do
          dataset = adapter.fetch(after: Date.new(2025, 1, 2), upto: Date.new(2025, 1, 8))
          days = dataset.group_by { |r| r[:date] }
          eur = dataset.select { |r| r[:base] == "EUR" }.to_h { |r| [r[:date], r[:rate]] }

          _(days.keys.sort).must_equal([Date.new(2025, 1, 3), Date.new(2025, 1, 6), Date.new(2025, 1, 8)])
          _(days.values.map(&:size).uniq).must_equal([25])
          _(eur[Date.new(2025, 1, 6)]).must_equal(eur[Date.new(2025, 1, 3)])
        end
      end

      it "returns nothing for a window that ends before coverage" do
        _(adapter.fetch(after: Date.new(2002, 1, 1), upto: Date.new(2002, 5, 14))).must_be_empty
      end

      it "parses rates from CSV" do
        csv = <<~CSV
          CreateDate, Date To, Code, Country, Currency, Unit, Middle Rate
          19.10.2026,19.10.2026,978,EMU,EUR,1,117.4109
          19.10.2026,19.10.2026,960,,XDR,1,141.8068
        CSV

        records = adapter.parse(csv)

        _(records).must_equal([
          { date: Date.new(2026, 10, 19), base: "EUR", quote: "RSD", rate: 117.4109 },
          { date: Date.new(2026, 10, 19), base: "XDR", quote: "RSD", rate: 141.8068 },
        ])
      end

      it "normalizes rates quoted per 100 units" do
        csv = "header\r\n19.10.2026,19.10.2026,392,Japan,JPY,100,66.1172\r\n"

        _(adapter.parse(csv).first[:rate]).must_be_close_to(0.661172, 1e-9)
      end

      it "excludes the euro's legacy currencies" do
        csv = <<~CSV
          header
          19.10.2026,19.10.2026,280,Germany,DEM,1,60.0312
          19.10.2026,19.10.2026,724,Spain,ESP,100,70.5654
          19.10.2026,19.10.2026,978,EMU,EUR,1,117.4109
        CSV

        _(adapter.parse(csv).map { |r| r[:base] }).must_equal(["EUR"])
      end

      it "dates rows by application day" do
        csv = "header\n06.01.2006,09.01.2006,978,EMU,EUR,1,86.9059\n"

        _(adapter.parse(csv).first[:date]).must_equal(Date.new(2006, 1, 9))
      end

      it "returns nothing for a header-only response" do
        csv = "Датум формирања, Датум примене, Валута, Назив земље, Ознака, Важи за, Средњи курс\r\n"

        _(adapter.parse(csv)).must_be_empty
      end
    end
  end
end
