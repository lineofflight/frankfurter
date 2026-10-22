# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bi"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BI do
      before do
        VCR.insert_cassette("bi", match_requests_on: [:method, :host], allow_playback_repeats: true)
      end

      after do
        VCR.eject_cassette
      end

      let(:adapter) { BI.new }

      def chart_page(*rows)
        data = rows.map do |code, qty, buy, sell, period|
          { mataUang: "#{code}  ", qty:, beli: buy, jual: sell, TimePeriod: "#{period} 12:00:00 AM" }
        end

        <<~HTML
          <script>
            function renderHighchart2(data, lang) {}
            if (typeof renderHighchart2 === 'function') {
              renderHighchart2(#{JSON.generate(data)}, 'en');
            }
          </script>
        HTML
      end

      # One currency keeps the cassette small: each page carries a quarter-megabyte view state.
      it "fetches rates" do
        dataset = adapter.stub(:extract_currencies, ["JPY  "]) do
          adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 5))
        end

        _(dataset.map { |r| r[:date] }).must_equal((Date.new(2026, 3, 2)..Date.new(2026, 3, 5)).to_a)
        _(dataset.map { |r| r[:base] }.uniq).must_equal(["JPY"])
        _(dataset.first[:rate]).must_be_close_to(107.49915)
      end

      it "stores foreign currency as base and IDR as quote" do
        records = adapter.parse(chart_page(["AED", 1, 4580.24, 4627.79, "3/5/2026"]), currency: "AED  ")

        _(records.first[:base]).must_equal("AED")
        _(records.first[:quote]).must_equal("IDR")
        _(records.first[:date]).must_equal(Date.new(2026, 3, 5))
      end

      it "computes mid-rate from buy and sell" do
        records = adapter.parse(chart_page(["AED", 1, 4580.24, 4627.79, "3/5/2026"]), currency: "AED  ")

        _(records.first[:rate]).must_equal(4604.015)
      end

      it "normalises JPY from per-100 to per-unit" do
        records = adapter.parse(chart_page(["JPY", 100, 10695.13, 10804.7, "3/2/2026"]), currency: "JPY  ")

        _(records.first[:rate]).must_be_close_to(107.49915)
        _(records.first[:bid]).must_be_close_to(106.9513)
        _(records.first[:ask]).must_be_close_to(108.047)
      end

      it "returns empty for a window without working days" do
        records = adapter.parse(chart_page, currency: "USD  ")

        _(records).must_be_empty
      end

      it "raises when the response has no chart data" do
        error = assert_raises(RuntimeError) do
          adapter.parse("<html><body>No data</body></html>", currency: "USD  ")
        end

        _(error.message).must_match(/no chart data/)
      end
    end
  end
end
