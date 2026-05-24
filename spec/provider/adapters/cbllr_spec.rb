# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/cbllr"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe CBLLR do
      before do
        VCR.insert_cassette("cbllr", match_requests_on: [:method, :host], allow_playback_repeats: true)
      end

      after { VCR.eject_cassette }

      let(:adapter) { CBLLR.new }

      it "fetches rates within a narrow date range" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 15), upto: Date.new(2026, 5, 22))

        _(dataset).wont_be_empty
        _(dataset.first[:base]).must_equal("USD")
        _(dataset.first[:quote]).must_equal("LRD")
        _(dataset.first[:rate]).must_be(:>, 0)
      end

      it "filters dates to requested range" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 15), upto: Date.new(2026, 5, 22))
        dates = dataset.map { |r| r[:date] }

        _(dates.min).must_be(:>, Date.new(2026, 5, 15))
        _(dates.max).must_be(:<=, Date.new(2026, 5, 22))
      end

      it "returns records sorted by date ascending" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 15), upto: Date.new(2026, 5, 22))
        dates = dataset.map { |r| r[:date] }

        _(dates).must_equal(dates.sort)
      end

      it "parses rate rows and coerces buy/sell to mid" do
        html = <<~HTML
          <table>
            <tbody>
              <tr>
                <td class="views-field views-field-field-content-post-date">
                  <time datetime="2026-05-21T12:00:00Z">Thursday, May 21, 2026</time>
                </td>
                <td class="views-field views-field-field-buying-us">L$182.0000/US$1.00</td>
                <td class="views-field views-field-field-selling-us">L$184.0000/US$1.00</td>
              </tr>
              <tr>
                <td class="views-field views-field-field-content-post-date">
                  <time datetime="2026-05-20T12:00:00Z">Wednesday, May 20, 2026</time>
                </td>
                <td class="views-field views-field-field-buying-us">L$181.5000/US$1.00</td>
                <td class="views-field views-field-field-selling-us">L$183.5000/US$1.00</td>
              </tr>
            </tbody>
          </table>
        HTML

        records = adapter.parse(html)

        _(records.length).must_equal(2)
        _(records.first[:date]).must_equal(Date.new(2026, 5, 21))
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("LRD")
        _(records.first[:rate]).must_equal(183.0)
        _(records.last[:rate]).must_equal(182.5)
      end

      it "skips rows missing rate cells" do
        html = <<~HTML
          <table>
            <tbody>
              <tr>
                <td class="views-field views-field-field-content-post-date">
                  <time datetime="2026-05-21T12:00:00Z">Thursday, May 21, 2026</time>
                </td>
              </tr>
              <tr>
                <td class="views-field views-field-field-content-post-date">
                  <time datetime="2026-05-20T12:00:00Z">Wednesday, May 20, 2026</time>
                </td>
                <td class="views-field views-field-field-buying-us">L$181.5000/US$1.00</td>
                <td class="views-field views-field-field-selling-us">L$183.5000/US$1.00</td>
              </tr>
            </tbody>
          </table>
        HTML

        records = adapter.parse(html)

        _(records.length).must_equal(1)
        _(records.first[:date]).must_equal(Date.new(2026, 5, 20))
      end

      it "skips rows with unparseable rate values" do
        html = <<~HTML
          <table>
            <tbody>
              <tr>
                <td class="views-field views-field-field-content-post-date">
                  <time datetime="2026-05-21T12:00:00Z">Thursday, May 21, 2026</time>
                </td>
                <td class="views-field views-field-field-buying-us">N/A</td>
                <td class="views-field views-field-field-selling-us">N/A</td>
              </tr>
            </tbody>
          </table>
        HTML

        _(adapter.parse(html)).must_be_empty
      end
    end
  end
end
