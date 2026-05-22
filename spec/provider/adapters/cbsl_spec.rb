# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/cbsl"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe CBSL do
      before do
        VCR.insert_cassette("cbsl", match_requests_on: [:method, :host], allow_playback_repeats: true)
      end

      after { VCR.eject_cassette }

      let(:adapter) { CBSL.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2025, 5, 15), upto: Date.new(2025, 5, 19))

        _(dataset).wont_be_empty
        _(dataset.first[:quote]).must_equal("LKR")
        _(dataset.first[:rate]).must_be(:>, 0)
      end

      it "emits multiple currencies as base against LKR" do
        dataset = adapter.fetch(after: Date.new(2025, 5, 15), upto: Date.new(2025, 5, 19))
        bases = dataset.map { |r| r[:base] }.uniq

        _(bases).must_include("USD")
        _(bases).must_include("EUR")
        _(bases).must_include("GBP")
      end

      it "filters dates to requested range" do
        dataset = adapter.fetch(after: Date.new(2025, 5, 15), upto: Date.new(2025, 5, 19))
        dates = dataset.map { |r| r[:date] }

        _(dates.min).must_be(:>=, Date.new(2025, 5, 15))
        _(dates.max).must_be(:<=, Date.new(2025, 5, 19))
      end

      it "emits XAU per troy ounce" do
        dataset = adapter.fetch(after: Date.new(2025, 5, 15), upto: Date.new(2025, 5, 19))
        xau = dataset.select { |r| r[:base] == "XAU" }

        _(xau).wont_be_empty
        # XAU should be in hundreds of thousands of LKR (per troy ounce), not tens of thousands (per gram)
        _(xau.first[:rate]).must_be(:>, 100_000)
      end

      it "parses HTML tables with currency code from the header" do
        html = <<~HTML
          <h2>US Dollar</h2>
          <table>
            <thead><tr>
              <th>Date</th>
              <th>1  USD  -&gt; LKR</th>
              <th>1 LKR -&gt;  USD </th>
            </tr></thead>
            <tbody>
              <tr><td> 2025-05-19 </td><td> 298.8165 </td><td> 0.0033 </td></tr>
              <tr><td> 2025-05-16 </td><td> 298.5101 </td><td> 0.0033 </td></tr>
            </tbody>
          </table>
          <h2>Euro</h2>
          <table>
            <thead><tr>
              <th>Date</th>
              <th>1  EUR  -&gt; LKR</th>
              <th>1 LKR -&gt;  EUR </th>
            </tr></thead>
            <tbody>
              <tr><td> 2025-05-19 </td><td> 334.3159 </td><td> 0.003 </td></tr>
            </tbody>
          </table>
        HTML

        records = adapter.parse(html)

        _(records.length).must_equal(3)
        usd = records.select { |r| r[:base] == "USD" }

        _(usd.length).must_equal(2)
        _(usd.first[:quote]).must_equal("LKR")
        _(usd.first[:rate]).must_equal(298.8165)
        _(usd.first[:date]).must_equal(Date.new(2025, 5, 19))

        eur = records.find { |r| r[:base] == "EUR" }

        _(eur[:rate]).must_equal(334.3159)
      end

      it "skips empty tbody sections" do
        html = <<~HTML
          <h2>US Dollar</h2>
          <table>
            <thead><tr><th>Date</th><th>1  USD  -&gt; LKR</th><th>1 LKR -&gt;  USD </th></tr></thead>
            <tbody>0 results</tbody>
          </table>
        HTML

        _(adapter.parse(html)).must_be_empty
      end

      it "skips entries with zero rates" do
        html = <<~HTML
          <h2>US Dollar</h2>
          <table>
            <thead><tr><th>Date</th><th>1  USD  -&gt; LKR</th><th>1 LKR -&gt;  USD </th></tr></thead>
            <tbody>
              <tr><td> 2025-05-19 </td><td> 0.0000 </td><td> 0 </td></tr>
            </tbody>
          </table>
        HTML

        _(adapter.parse(html)).must_be_empty
      end
    end
  end
end
