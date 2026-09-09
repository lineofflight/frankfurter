# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bcccd"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BCCCD do
      before do
        VCR.insert_cassette("bcccd", match_requests_on: [:method, :uri], allow_playback_repeats: true)
      end

      after { VCR.eject_cassette }

      let(:adapter) { BCCCD.new }
      let(:dataset) { adapter.fetch(after: Date.new(2026, 9, 2), upto: Date.new(2026, 9, 3)) }

      it "fetches rates with date range" do
        dates = dataset.map { |r| r[:date] }.uniq

        _(dates).must_equal([Date.new(2026, 9, 2), Date.new(2026, 9, 3)])
      end

      it "quotes the whole basket against CDF" do
        sample = dataset.select { |r| r[:date] == Date.new(2026, 9, 3) }

        _(sample.map { |r| r[:quote] }.uniq).must_equal(["CDF"])
        _(sample.map { |r| r[:base] }).must_include("USD")
        _(sample.map { |r| r[:base] }).must_include("RWF")
        _(sample.size).must_be(:>, 10)
      end

      it "prefers the dated page over the history for overlapping pairs" do
        usd = dataset.find { |r| r[:date] == Date.new(2026, 9, 3) && r[:base] == "USD" }
        day = adapter.parse_day(adapter.send(:fetch_day, Date.new(2026, 9, 3)), Date.new(2026, 9, 3))

        _(usd[:rate]).must_equal(day.find { |r| r[:base] == "USD" }[:rate])
      end

      it "returns records sorted by date" do
        dates = dataset.map { |r| r[:date] }

        _(dates).must_equal(dates.sort)
      end

      it "parses a dated page" do
        html = <<~HTML
          <dl>
            <div><dt>USD (cours moyen)</dt><dd>2265.71</dd></div>
            <div><dt>EUR (cours moyen)</dt><dd>2 630,6300</dd></div>
            <div><dt>Autre</dt><dd>1</dd></div>
          </dl>
        HTML
        records = adapter.parse_day(html, Date.new(2026, 9, 8))

        _(records.size).must_equal(2)
        _(records.first).must_equal({ date: Date.new(2026, 9, 8), base: "USD", quote: "CDF", rate: 2265.71 })
        _(records.last[:rate]).must_equal(2630.63)
      end

      it "returns nothing for a dated page without rows" do
        _(adapter.parse_day("<article><p>05 janvier 2021</p></article>", Date.new(2021, 1, 5))).must_be_empty
        _(adapter.parse_day("", Date.new(2021, 1, 5))).must_be_empty
      end

      it "parses the embedded history" do
        payload = <<~RSC
          "series":{"USD":[1980.6]},"history":{"USD":[{"date":"2021-02-24","buy":1941.025,"average":1980.6377,"sell":2020.2505,"unit":1}],"RWF":[{"date":"2021-02-24","buy":1.5,"average":1.54,"sell":1.57,"unit":1}]},"other":1
        RSC
        records = adapter.parse_history(payload)

        _(records.size).must_equal(2)
        _(records.first).must_equal({ date: Date.new(2021, 2, 24), base: "USD", quote: "CDF", rate: 1980.6377 })
        _(records.last[:base]).must_equal("RWF")
      end

      it "normalizes history rows by unit and skips empty ones" do
        payload = '"history":{"JPY":[{"date":"2026-09-09","average":1471,"unit":100},' \
                  '{"date":"2026-09-08","average":null,"unit":1}]}'
        records = adapter.parse_history(payload)

        _(records.size).must_equal(1)
        _(records.first[:rate]).must_equal(14.71)
      end

      it "lets the later history entry win when a date repeats" do
        payload = '"history":{"USD":[{"date":"2025-11-03","average":2193.4902,"unit":1},' \
                  '{"date":"2025-11-03","average":2261.1793,"unit":1}]}'
        adapter.stub(:fetch_history, payload) do
          adapter.stub(:fetch_day, "") do
            records = adapter.fetch(after: Date.new(2025, 11, 3), upto: Date.new(2025, 11, 3))

            _(records.map { |r| r[:rate] }).must_equal([2261.1793])
          end
        end
      end

      it "raises when the history is missing" do
        _ { adapter.parse_history("<html></html>") }.must_raise(RuntimeError)
      end
    end
  end
end
