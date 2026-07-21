# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/nbc"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe NBC do
      before do
        VCR.insert_cassette("nbc", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { NBC.new }

      it "fetches rates for a date range" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 18), upto: Date.new(2026, 5, 20))

        _(dataset).wont_be_empty
      end

      it "returns multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 20), upto: Date.new(2026, 5, 20))
        dates = dataset.map { |r| r[:date] }.uniq

        _(dates.size).must_equal(1)
        _(dataset.size).must_be(:>, 10)
      end

      it "includes USD from the headline official exchange rate" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 20), upto: Date.new(2026, 5, 20))

        _(dataset.any? { |r| r[:base] == "USD" && r[:quote] == "KHR" }).must_equal(true)
      end

      it "parses unit-1 rows directly" do
        html = <<~HTML
          <table>
            <tr>
              <td>European Euro</td>
              <td align='center'>EUR/KHR</td>
              <td align='center'>1</td>
              <td align='right'>4678</td>
              <td align='right'>4725</td>
              <td align='right'>4701.50</td>
            </tr>
          </table>
        HTML
        records = adapter.parse(html, date: Date.new(2026, 5, 20))

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("EUR")
        _(records.first[:quote]).must_equal("KHR")
        _(records.first[:rate]).must_be_close_to(4701.5, 0.0001)
      end

      it "normalizes per-100 unit rows" do
        html = <<~HTML
          <table>
            <tr>
              <td>Japanese Yen</td>
              <td align='center'>JPY/KHR</td>
              <td align='center'>100</td>
              <td align='right'>2529</td>
              <td align='right'>2554</td>
              <td align='right'>2541.50</td>
            </tr>
          </table>
        HTML
        records = adapter.parse(html, date: Date.new(2026, 5, 20))

        _(records.first[:base]).must_equal("JPY")
        _(records.first[:rate]).must_be_close_to(25.415, 0.0001)
      end

      it "normalizes per-1000 unit rows" do
        html = <<~HTML
          <table>
            <tr>
              <td>Vietnamese Dong</td>
              <td align='center'>VND/KHR</td>
              <td align='center'>1000</td>
              <td align='right'>153</td>
              <td align='right'>154</td>
              <td align='right'>153.50</td>
            </tr>
          </table>
        HTML
        records = adapter.parse(html, date: Date.new(2026, 5, 20))

        _(records.first[:base]).must_equal("VND")
        _(records.first[:rate]).must_be_close_to(0.1535, 0.0001)
      end

      it "rewrites SDR to XDR" do
        html = <<~HTML
          <table>
            <tr>
              <td>Special Drawing Right</td>
              <td align='center'>SDR/KHR</td>
              <td align='center'>1</td>
              <td align='right'>5498</td>
              <td align='right'>5553</td>
              <td align='right'>5525.50</td>
            </tr>
          </table>
        HTML
        records = adapter.parse(html, date: Date.new(2026, 5, 20))

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("XDR")
        _(records.first[:quote]).must_equal("KHR")
        _(records.first[:rate]).must_be_close_to(5525.5, 0.0001)
      end

      it "extracts USD from the headline KHR/USD line" do
        html = <<~HTML
          <table>
            <tr><td>Official Exchange Rate : <font color="#FF3300">4022</font> KHR / USD</td></tr>
          </table>
          <table>
            <tr>
              <td>European Euro</td>
              <td align='center'>EUR/KHR</td>
              <td align='center'>1</td>
              <td align='right'>4678</td>
              <td align='right'>4725</td>
              <td align='right'>4701.50</td>
            </tr>
          </table>
        HTML
        records = adapter.parse(html, date: Date.new(2026, 5, 20))
        usd = records.find { |r| r[:base] == "USD" }

        _(usd).wont_be_nil
        _(usd[:quote]).must_equal("KHR")
        _(usd[:rate]).must_equal(4022.0)
      end

      it "returns empty for no-data responses" do
        html = <<~HTML
          <table>
            <tr><td colspan='6'>There is no data available.</td></tr>
          </table>
        HTML
        records = adapter.parse(html, date: Date.new(2026, 5, 17))

        _(records).must_be_empty
      end

      # A WAF 403 carries no rate table, so parsing it would look identical to a holiday and
      # mint a permanent hole once last_synced moved past the date. Raise instead.
      it "raises when the POST is blocked" do
        VCR.eject_cassette

        begin
          VCR.turned_off do
            WebMock.stub_request(:get, /www\.nbc\.gov\.kh/)
              .to_return(status: 200, body: "<input name='tk' value='abc'>")
            WebMock.stub_request(:post, /www\.nbc\.gov\.kh/)
              .to_return(status: 403, body: "<html><body>Request blocked</body></html>")

            _(-> { adapter.fetch(after: Date.new(2026, 5, 20), upto: Date.new(2026, 5, 20)) })
              .must_raise(HTTP::StatusError)
          end
        ensure
          WebMock.reset!
          VCR.insert_cassette("nbc", match_requests_on: [:method, :host])
        end
      end

      it "raises when the landing page is blocked" do
        VCR.eject_cassette

        begin
          VCR.turned_off do
            WebMock.stub_request(:get, /www\.nbc\.gov\.kh/).to_return(status: 403, body: "blocked")

            _(-> { adapter.fetch(after: Date.new(2026, 5, 20), upto: Date.new(2026, 5, 20)) })
              .must_raise(HTTP::StatusError)
          end
        ensure
          WebMock.reset!
          VCR.insert_cassette("nbc", match_requests_on: [:method, :host])
        end
      end

      it "skips Sundays in the date range" do
        # 2026-05-17 is a Sunday — should be skipped without raising even if the API would error.
        dataset = adapter.fetch(after: Date.new(2026, 5, 17), upto: Date.new(2026, 5, 17))

        _(dataset).must_be_empty
      end
    end
  end
end
