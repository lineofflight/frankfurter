# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/rbv"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe RBV do
      before do
        VCR.insert_cassette("rbv", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { RBV.new }

      it "fetches rates within a date range" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 18), upto: Date.new(2026, 5, 22))

        _(dataset).wont_be_empty
        _(dataset.map { |r| r[:date] }.uniq).must_equal(
          dataset.map { |r| r[:date] }.uniq.sort.reverse,
        )
      end

      it "emits all six basket currencies for a published date" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 21), upto: Date.new(2026, 5, 22))
        codes = dataset.map { |r| r[:base] }.uniq.sort

        _(codes).must_equal(["AUD", "EUR", "GBP", "JPY", "NZD", "USD"])
      end

      it "parses VUV as the quote and foreign currency as the base" do
        html = <<~HTML
          <table>
            <tr class="fabrik_row">
              <td class="exchange_rates___date fabrik_element">22 May 2026</td>
              <td class="exchange_rates___usd fabrik_element decimal">116.28</td>
              <td class="exchange_rates___jpy fabrik_element decimal">0.7316</td>
              <td class="exchange_rates___nzd fabrik_element decimal">68.37</td>
              <td class="exchange_rates___GBP fabrik_element decimal">156.19</td>
              <td class="exchange_rates___aud fabrik_element decimal">83.16</td>
              <td class="exchange_rates___eur fabrik_element decimal">135.14</td>
            </tr>
          </table>
        HTML
        records = adapter.parse(html)

        _(records.length).must_equal(6)
        usd = records.find { |r| r[:base] == "USD" }
        _(usd[:quote]).must_equal("VUV")
        _(usd[:rate]).must_be_close_to(116.28, 0.001)
        _(usd[:date]).must_equal(Date.new(2026, 5, 22))
      end

      it "parses the older dd-Mon-yy date format" do
        html = <<~HTML
          <table>
            <tr class="fabrik_row">
              <td class="exchange_rates___date fabrik_element">26-Aug-25</td>
              <td class="exchange_rates___usd fabrik_element decimal">119.06</td>
              <td class="exchange_rates___jpy fabrik_element decimal">0.8058</td>
              <td class="exchange_rates___nzd fabrik_element decimal">69.63</td>
              <td class="exchange_rates___GBP fabrik_element decimal">160.23</td>
              <td class="exchange_rates___aud fabrik_element decimal">77.17</td>
              <td class="exchange_rates___eur fabrik_element decimal">138.33</td>
            </tr>
          </table>
        HTML
        records = adapter.parse(html)

        _(records.first[:date]).must_equal(Date.new(2025, 8, 26))
      end

      it "skips rows with non-positive rates" do
        html = <<~HTML
          <table>
            <tr class="fabrik_row">
              <td class="exchange_rates___date fabrik_element">22 May 2026</td>
              <td class="exchange_rates___usd fabrik_element decimal">0</td>
              <td class="exchange_rates___jpy fabrik_element decimal">0.7316</td>
              <td class="exchange_rates___nzd fabrik_element decimal"></td>
              <td class="exchange_rates___GBP fabrik_element decimal">156.19</td>
              <td class="exchange_rates___aud fabrik_element decimal">83.16</td>
              <td class="exchange_rates___eur fabrik_element decimal">135.14</td>
            </tr>
          </table>
        HTML
        records = adapter.parse(html)
        codes = records.map { |r| r[:base] }.sort

        _(codes).must_equal(["AUD", "EUR", "GBP", "JPY"])
      end

      it "skips rows with unparseable dates" do
        html = <<~HTML
          <table>
            <tr class="fabrik_row">
              <td class="exchange_rates___date fabrik_element">not a date</td>
              <td class="exchange_rates___usd fabrik_element decimal">116.28</td>
            </tr>
          </table>
        HTML
        records = adapter.parse(html)

        _(records).must_be_empty
      end
    end
  end
end
