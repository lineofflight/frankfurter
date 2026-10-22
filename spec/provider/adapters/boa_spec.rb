# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/boa"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BOA do
      before do
        VCR.insert_cassette("boa", match_requests_on: [:method, :host, :path])
      end

      after { VCR.eject_cassette }

      let(:adapter) { BOA.new }

      it "fetches rates with DZD as the quote currency" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 28), upto: Date.new(2026, 4, 30))

        _(dataset).wont_be_empty
        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["DZD"])
      end

      it "covers multiple base currencies in a single fetch" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 28), upto: Date.new(2026, 4, 30))

        bases = dataset.map { |r| r[:base] }.uniq.sort

        _(bases).must_include("USD")
        _(bases).must_include("EUR")
        _(bases).must_include("GBP")
        _(bases).must_include("JPY")
      end

      it "maps the EURO sheet to EUR" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 28), upto: Date.new(2026, 4, 30))
        eur = dataset.find { |r| r[:base] == "EUR" && r[:date] == Date.new(2026, 4, 30) }

        _(eur).wont_be_nil
        _(eur[:rate]).must_be_close_to(154.76, 0.5)
      end

      it "normalises JPY from per-100 to per-unit" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 28), upto: Date.new(2026, 4, 30))
        jpy = dataset.find { |r| r[:base] == "JPY" && r[:date] == Date.new(2026, 4, 30) }

        _(jpy).wont_be_nil
        _(jpy[:rate]).must_be_close_to(0.828, 0.05)
      end

      it "filters records by the requested date range" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 28), upto: Date.new(2026, 4, 30))

        _(dataset.map { |r| r[:date] }.min).must_equal(Date.new(2026, 4, 28))
        _(dataset.map { |r| r[:date] }.max).must_equal(Date.new(2026, 4, 30))
      end

      it "returns USD/DZD in a plausible range" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 30), upto: Date.new(2026, 4, 30))
        usd = dataset.find { |r| r[:base] == "USD" }

        _(usd).wont_be_nil
        _(usd[:rate]).must_be_close_to(132.5, 5.0)
      end

      it "finds an archive link with a prefixed file name" do
        url = "https://www.bank-of-algeria.dz/stoodroa/2026/10/clean_Cotation-DZD-2000-2026-journalier.xlsx"
        hub = %(<a href="https://www.bank-of-algeria.dz/stoodroa/2026/01/Cours-moyens-annuels-1994-2025.xlsx">
          <a href="#{url}">)

        adapter.stub(:download, hub) do
          _(adapter.send(:locate_archive_url)).must_equal(url)
        end
      end

      it "raises when the archive link is missing from the hub page" do
        adapter.stub(:download, "<html><body>no link here</body></html>") do
          assert_raises(RuntimeError) { adapter.fetch }
        end
      end
    end
  end
end
