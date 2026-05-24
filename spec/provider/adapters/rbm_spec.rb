# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/rbm"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe RBM do
      before do
        VCR.insert_cassette("rbm", match_requests_on: [:method, :host])
      end

      after do
        VCR.eject_cassette
      end

      let(:adapter) { RBM.new }

      it "fetches rates across the requested date range" do
        dataset = adapter.fetch(after: Date.new(2024, 1, 2), upto: Date.new(2024, 1, 5))

        _(dataset).wont_be_empty

        dates = dataset.map { |r| r[:date] }.uniq.sort

        _(dates).must_include(Date.new(2024, 1, 2))
        _(dates).must_include(Date.new(2024, 1, 5))
      end

      it "stores foreign currency as base and MWK as quote" do
        dataset = adapter.fetch(after: Date.new(2024, 1, 2), upto: Date.new(2024, 1, 5))

        usd = dataset.find { |r| r[:base] == "USD" && r[:date] == Date.new(2024, 1, 2) }

        _(usd).wont_be_nil
        _(usd[:quote]).must_equal("MWK")
        _(usd[:rate]).must_be(:>, 1_000)
      end

      it "covers a broad set of quote currencies" do
        dataset = adapter.fetch(after: Date.new(2024, 1, 2), upto: Date.new(2024, 1, 5))

        codes = dataset.map { |r| r[:base] }.uniq

        ["USD", "EUR", "GBP", "ZAR", "ZMW", "MZN", "XDR"].each do |code|
          _(codes).must_include(code)
        end
      end

      it "parses currency code, middle rate, and date from a table row" do
        html = <<~HTML
          <table id="exchange-rates" class="table table-striped table-bordered">
            <tr>
              <td><strong>USD</strong></td>
              <td>1,665.0099</td>
              <td>1,683.3700</td>
              <td>1,701.7300</td>
              <td style="color: #2670a2;"><span>Jan 02&nbsp;&nbsp;</span><span>2024</span></td>
            </tr>
          </table>
        HTML

        records = adapter.parse(html)

        _(records.length).must_equal(1)
        _(records.first[:date]).must_equal(Date.new(2024, 1, 2))
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("MWK")
        _(records.first[:rate]).must_be_close_to(1683.37, 0.001)
      end

      it "skips rows without a recognizable currency code" do
        html = <<~HTML
          <table id="exchange-rates">
            <tr>
              <td><strong>Currency</strong></td>
              <td>Buying</td>
              <td>Middle</td>
              <td>Selling</td>
              <td>Date</td>
            </tr>
            <tr>
              <td><strong>USD</strong></td>
              <td>1,665.0099</td>
              <td>1,683.3700</td>
              <td>1,701.7300</td>
              <td><span>Jan 02&nbsp;&nbsp;</span><span>2024</span></td>
            </tr>
          </table>
        HTML

        records = adapter.parse(html)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
      end

      it "skips rows whose middle rate is missing or zero" do
        html = <<~HTML
          <table id="exchange-rates">
            <tr>
              <td><strong>USD</strong></td>
              <td>1,665.0099</td>
              <td>0</td>
              <td>1,701.7300</td>
              <td><span>Jan 02&nbsp;&nbsp;</span><span>2024</span></td>
            </tr>
            <tr>
              <td><strong>EUR</strong></td>
              <td>1,800</td>
              <td></td>
              <td>1,900</td>
              <td><span>Jan 02&nbsp;&nbsp;</span><span>2024</span></td>
            </tr>
          </table>
        HTML

        records = adapter.parse(html)

        _(records).must_be_empty
      end

      it "returns an empty array when no table is present" do
        records = adapter.parse("<html><body>no data</body></html>")

        _(records).must_be_empty
      end

      it "returns an empty array when start_date is after upto" do
        records = adapter.fetch(after: Date.new(2024, 1, 10), upto: Date.new(2024, 1, 5))

        _(records).must_be_empty
      end
    end
  end
end
