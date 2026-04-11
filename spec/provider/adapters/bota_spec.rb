# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bota"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BOTA do
      before do
        VCR.insert_cassette("bota", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { BOTA.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 24), upto: Date.new(2026, 3, 28))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 24), upto: Date.new(2026, 3, 28))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "parses HTML table with correct base and quote" do
        html = <<~HTML
          <html><body>
          <table><tbody>
          <tr>
            <td>1</td><td>USD</td><td>2568.72</td><td>2594.41</td><td>2581.57</td><td>24-Mar-26</td>
          </tr>
          </tbody></table>
          </body></html>
        HTML

        records = adapter.parse(html)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("TZS")
        _(records.first[:rate]).must_equal(2581.57)
        _(records.first[:date]).must_equal(Date.new(2026, 3, 24))
      end

      it "excludes GOLD and defunct currencies" do
        html = <<~HTML
          <html><body>
          <table><tbody>
          <tr><td>1</td><td>USD</td><td>2568.72</td><td>2594.41</td><td>2581.57</td><td>24-Mar-26</td></tr>
          <tr><td>2</td><td>GOLD</td><td>1000.00</td><td>2000.00</td><td>1500.00</td><td>24-Mar-26</td></tr>
          <tr><td>3</td><td>ATS</td><td>100.00</td><td>200.00</td><td>150.00</td><td>24-Mar-26</td></tr>
          <tr><td>4</td><td>NLG</td><td>100.00</td><td>200.00</td><td>150.00</td><td>24-Mar-26</td></tr>
          <tr><td>5</td><td>MZM</td><td>100.00</td><td>200.00</td><td>150.00</td><td>24-Mar-26</td></tr>
          <tr><td>6</td><td>ZWD</td><td>100.00</td><td>200.00</td><td>150.00</td><td>24-Mar-26</td></tr>
          <tr><td>7</td><td>CUC</td><td>100.00</td><td>200.00</td><td>150.00</td><td>24-Mar-26</td></tr>
          <tr><td>8</td><td>EUR</td><td>2950.95</td><td>2980.46</td><td>2965.70</td><td>24-Mar-26</td></tr>
          </tbody></table>
          </body></html>
        HTML

        records = adapter.parse(html)
        currencies = records.map { |r| r[:base] }

        _(currencies).must_include("USD")
        _(currencies).must_include("EUR")
        _(currencies).wont_include("GOLD")
        _(currencies).wont_include("ATS")
        _(currencies).wont_include("NLG")
        _(currencies).wont_include("MZM")
        _(currencies).wont_include("ZWD")
        _(currencies).wont_include("CUC")
        _(records.length).must_equal(2)
      end

      it "handles rates with commas in numbers" do
        html = <<~HTML
          <html><body>
          <table><tbody>
          <tr><td>1</td><td>GOLD</td><td>11,759,124.79</td><td>11,876,716.04</td><td>11,817,920.42</td><td>24-Mar-26</td></tr>
          <tr><td>2</td><td>JPY</td><td>17.1234</td><td>17.2946</td><td>17.2090</td><td>24-Mar-26</td></tr>
          </tbody></table>
          </body></html>
        HTML

        records = adapter.parse(html)

        # GOLD is excluded
        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("JPY")
        _(records.first[:rate]).must_equal(17.209)
      end
    end
  end
end
