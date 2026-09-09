# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/cbo"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe CBO do
      before do
        VCR.insert_cassette("cbo", match_requests_on: [:method, :uri, :body], allow_playback_repeats: true)
      end

      after { VCR.eject_cassette }

      let(:adapter) { CBO.new }
      let(:dataset) { adapter.fetch(after: Date.new(2026, 6, 1), upto: Date.new(2026, 6, 4)) }

      it "fetches rates" do
        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 40)
      end

      it "quotes OMR per unit of foreign currency" do
        usd = dataset.find { |r| r[:base] == "USD" && r[:date] == Date.new(2026, 6, 1) }

        _(usd[:quote]).must_equal("OMR")
        _(usd[:rate]).must_equal(0.3845)
      end

      it "includes precious metals per ounce" do
        xau = dataset.find { |r| r[:base] == "XAU" }

        _(xau).wont_be_nil
        _(xau[:rate]).must_be(:>, 100)
      end

      it "respects date boundaries" do
        _(dataset.map { |r| r[:date] }.uniq.sort).must_equal([1, 2, 3, 4].map { |d| Date.new(2026, 6, d) })
      end

      it "parses the export table" do
        records = adapter.parse(export(<<~ROWS))
          <tr><td>USD</td><td>01/06/2026 10:00:00 AM</td><td>United States Dollar</td><td>x</td><td>string;#0.384</td><td>string;#0.385</td></tr>
          <tr><td>USD</td><td>02/06/2026 09:55:00 AM</td><td>United States Dollar</td><td>x</td><td>string;#0.384</td><td>string;#0.385</td></tr>
        ROWS

        _(records.length).must_equal(2)
        _(records.first).must_equal({ date: Date.new(2026, 6, 1), base: "USD", quote: "OMR", rate: 0.3845 })
      end

      it "keeps the latest intraday revision per date" do
        records = adapter.parse(export(<<~ROWS))
          <tr><td>EUR</td><td>26/05/2019 08:21:48 AM</td><td>Euro*</td><td>x</td><td>string;#0.4276608</td><td>string;#0.4288515</td></tr>
          <tr><td>EUR</td><td>26/05/2019 07:03:44 AM</td><td>Euro*</td><td>x</td><td>string;#0.4301184</td><td>string;#0.4313155</td></tr>
        ROWS

        _(records.length).must_equal(1)
        _(records.first[:rate]).must_equal(0.42825615)
      end

      it "skips rows without a usable buy and sell" do
        records = adapter.parse(export(<<~ROWS))
          <tr><td>USD</td><td>01/06/2026 10:00:00 AM</td><td>United States Dollar</td><td>x</td><td>string;#</td><td>string;#0.385</td></tr>
          <tr><td>USD</td><td>02/06/2026 10:00:00 AM</td><td>United States Dollar</td><td>x</td><td>string;#0</td><td>string;#0.385</td></tr>
        ROWS

        _(records).must_be_empty
      end

      it "returns empty for a window with no rates" do
        _(adapter.parse(export(""))).must_be_empty
      end

      it "raises when the export is not the rates table" do
        error = assert_raises(RuntimeError) { adapter.parse("<html><body>DFESearch</body></html>") }

        _(error.message).must_match(/CBO/)
      end

      def export(rows)
        <<~HTML
          <table>
            <tr style="font-weight:bold;">
              <td>Currency Code</td><td>Exchange Date</td><td>Currency Name (English)</td><td>Country Name</td><td>Buying</td><td>Selling</td>
            </tr>
            #{rows}
          </table>
        HTML
      end
    end
  end
end
