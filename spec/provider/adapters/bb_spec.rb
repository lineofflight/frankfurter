# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bb"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BB do
      before do
        VCR.insert_cassette("bb", match_requests_on: [:method, :uri, :body])
      end

      after { VCR.eject_cassette }

      let(:adapter) { BB.new }
      let(:dataset) { adapter.fetch(after: Date.new(2026, 11, 1), upto: Date.new(2026, 11, 5)) }

      it "fetches rates within the window" do
        _(dataset.map { |r| r[:date] }.uniq.sort).must_equal([2, 3, 4, 5].map { |d| Date.new(2026, 11, d) })
      end

      it "fetches eleven currencies per date" do
        sample = dataset.select { |r| r[:date] == Date.new(2026, 11, 5) }

        _(sample.map { |r| r[:base] == "BDT" ? r[:quote] : r[:base] }.sort).must_equal(
          ["AUD", "CAD", "CNH", "EUR", "GBP", "INR", "JPY", "LKR", "SEK", "SGD", "USD"],
        )
      end

      it "takes the weighted average as the USD rate" do
        usd = dataset.find { |r| r[:base] == "USD" && r[:date] == Date.new(2026, 11, 5) }

        _(usd).must_equal({ date: Date.new(2026, 11, 5), base: "USD", quote: "BDT", rate: 124.34, bid: 124.32,
                            ask: 124.35, mid: 124.34, })
      end

      it "quotes LKR per taka" do
        lkr = dataset.find { |r| r[:quote] == "LKR" }

        _(lkr[:base]).must_equal("BDT")
        _(lkr[:rate]).must_be(:>, 1)
      end

      it "returns nothing for a window that ends before coverage" do
        _(adapter.fetch(after: Date.new(2006, 1, 1), upto: Date.new(2006, 3, 20))).must_be_empty
      end

      it "requests every month the window touches" do
        months = adapter.send(:months, Date.new(2026, 10, 28), Date.new(2026, 11, 2))

        _(months).must_equal([Date.new(2026, 10, 1), Date.new(2026, 11, 1)])
      end

      it "resolves bid and ask to a mid without a weighted average" do
        records = adapter.parse(page(<<~ROWS))
          <tr><td style='position: sticky;'>30/01/20</td><td>84.9000</td><td>84.9000</td><td></td><td>93.4664</td><td>93.5004</td><td></td><td></td><tr>
        ROWS

        _(records).must_equal([
          { date: Date.new(2020, 1, 30), base: "USD", quote: "BDT", rate: 84.9, bid: 84.9, ask: 84.9, mid: nil },
          { date: Date.new(2020, 1, 30), base: "EUR", quote: "BDT", rate: 93.4834, bid: 93.4664, ask: 93.5004,
            mid: nil, },
        ])
      end

      it "skips currencies without a deal that day" do
        records = adapter.parse(page(<<~ROWS))
          <tr><td style='position: sticky;'>05/11/26</td><td>124.3200</td><td>124.3500</td><td>124.3400</td><td></td><td></td><td>2.6755</td><td>2.6770</td><tr>
        ROWS

        _(records.map { |r| [r[:base], r[:quote]] }).must_equal([["USD", "BDT"], ["BDT", "LKR"]])
      end

      it "returns nothing for a month without data" do
        _(adapter.parse(page("<td colspan='23'>Data not Available</td>"))).must_be_empty
      end

      it "raises when the page has no rates table" do
        error = assert_raises(RuntimeError) { adapter.parse("<html><body>Exchange Rate of Taka</body></html>") }

        _(error.message).must_match(/BB/)
      end

      def page(rows)
        <<~HTML
          <table><thead>
            <tr class="header-top"><th>Date</th><th colspan='3'>USD</th><th colspan='2'>EUR</th><th colspan='2'>LKR</th></tr>
            <tr><td></td><td>Bid Rate</td><td>Ask Rate</td><td>WAR</td><td>Bid Rate</td><td>Ask Rate</td><td>Bid Rate</td><td>Ask Rate</td></tr>
          </thead><tbody>#{rows}</tbody></table>
        HTML
      end
    end
  end
end
