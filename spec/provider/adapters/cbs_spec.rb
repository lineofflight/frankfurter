# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/cbs"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe CBS do
      before do
        VCR.insert_cassette("cbs", match_requests_on: [:method, :host, :path])
      end

      after do
        VCR.eject_cassette
      end

      let(:adapter) { CBS.new }

      it "fetches rates from the archive workbook" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 1), upto: Date.new(2026, 5, 22))

        _(dataset).wont_be_empty
      end

      it "emits WST as the base currency" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 1), upto: Date.new(2026, 5, 22))

        _(dataset.map { |r| r[:base] }.uniq).must_equal(["WST"])
      end

      it "covers the nine quote currencies CBS publishes" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 1), upto: Date.new(2026, 5, 22))
        quotes = dataset.map { |r| r[:quote] }.uniq.sort
        expected = ["AUD", "CNH", "CNY", "EUR", "FJD", "GBP", "JPY", "NZD", "USD"]

        expected.each { |iso| _(quotes).must_include(iso) }
      end

      it "respects the after and upto bounds" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 14), upto: Date.new(2026, 5, 17))
        dates = dataset.map { |r| r[:date] }.uniq.sort

        _(dates.min).must_be(:>=, Date.new(2026, 5, 14))
        _(dates.max).must_be(:<=, Date.new(2026, 5, 17))
      end

      it "returns USD/WST in a plausible range (less than 1 — tala is weaker than dollar)" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 1), upto: Date.new(2026, 5, 22))
        usd = dataset.find { |r| r[:quote] == "USD" }

        _(usd).wont_be_nil
        _(usd[:rate]).must_be(:>, 0.2)
        _(usd[:rate]).must_be(:<, 0.6)
      end

      it "covers each quoted date with a row per available currency" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 18), upto: Date.new(2026, 5, 22))
        dates = dataset.map { |r| r[:date] }.uniq

        dates.each do |date|
          quotes = dataset.select { |r| r[:date] == date }.map { |r| r[:quote] }

          _(quotes).must_include("USD")
          _(quotes).must_include("EUR")
        end
      end

      describe "#archive_url" do
        it "resolves the date-stamped workbook link from the data page" do
          html = <<~HTML
            <ul class="downloads">
              <li><a href="/media/Historical-Daily-Rates-June032026.xlsx">Historical rates</a></li>
            </ul>
          HTML

          _(adapter.archive_url(html)).must_equal(
            "https://cbs.gov.ws/media/Historical-Daily-Rates-June032026.xlsx",
          )
        end

        it "percent-encodes spaces in the workbook filename" do
          html = <<~HTML
            <ul class="downloads">
              <li><a href="/media/Historical Daily Rates-220626.xlsx">Historical rates</a></li>
            </ul>
          HTML

          _(adapter.archive_url(html)).must_equal(
            "https://cbs.gov.ws/media/Historical%20Daily%20Rates-220626.xlsx",
          )
        end

        it "raises when the page exposes no workbook link" do
          _ { adapter.archive_url("<html><body>no link here</body></html>") }
            .must_raise(Adapter::Unavailable)
        end
      end

      describe "#parse" do
        it "maps source column labels to ISO 4217 codes" do
          _(CBS::CURRENCIES["TALA/USD"]).must_equal("USD")
          _(CBS::CURRENCIES["TALA/EURO"]).must_equal("EUR")
          _(CBS::CURRENCIES["TALA/YEN"]).must_equal("JPY")
          _(CBS::CURRENCIES["TALA/CNY"]).must_equal("CNY")
          _(CBS::CURRENCIES["TALA/CNH"]).must_equal("CNH")
        end
      end
    end
  end
end
