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

      it "fetches rates" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 5))

        _(dataset).wont_be_empty
      end

      it "stores foreign currency as base and IDR as quote" do
        html = <<~HTML
          <table id="foo_gvSearchResult2">
          <tr><th>Value</th><th>Sell</th><th>Buy</th><th>Date</th></tr>
          <tr><td>1.00</td><td>4,627.79</td><td>4,580.24</td><td>5 Mar 2026</td></tr>
          </table>
        HTML

        records = adapter.parse(html, currency: "AED  ")

        _(records.first[:base]).must_equal("AED")
        _(records.first[:quote]).must_equal("IDR")
      end

      it "computes mid-rate from buy and sell" do
        html = <<~HTML
          <table id="foo_gvSearchResult2">
          <tr><th>Value</th><th>Sell</th><th>Buy</th><th>Date</th></tr>
          <tr><td>1.00</td><td>4,627.79</td><td>4,580.24</td><td>5 Mar 2026</td></tr>
          </table>
        HTML

        records = adapter.parse(html, currency: "AED  ")

        _(records.first[:rate]).must_equal(4604.015)
      end

      it "parses rates with thousands separators" do
        html = <<~HTML
          <table id="foo_gvSearchResult2">
          <tr><th>Value</th><th>Sell</th><th>Buy</th><th>Date</th></tr>
          <tr><td>1.00</td><td>19,743.74</td><td>19,543.91</td><td>3 Mar 2026</td></tr>
          </table>
        HTML

        records = adapter.parse(html, currency: "EUR  ")

        _(records.first[:rate]).must_equal(19643.825)
      end

      it "returns empty for missing table" do
        records = adapter.parse("<html><body>No data</body></html>", currency: "USD  ")

        _(records).must_be_empty
      end
    end
  end
end
