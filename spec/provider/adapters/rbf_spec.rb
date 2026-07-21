# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/rbf"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe RBF do
      before do
        VCR.insert_cassette("rbf", match_requests_on: [:method, :host, :path])
      end

      after { VCR.eject_cassette }

      let(:adapter) { RBF.new }

      it "fetches rates with FJD as the base currency" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 19), upto: Date.new(2026, 5, 22))

        _(dataset).wont_be_empty
        _(dataset.map { |r| r[:base] }.uniq).must_equal(["FJD"])
      end

      it "covers all eight quote currencies in a single fetch" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 19), upto: Date.new(2026, 5, 22))
        quotes = dataset.map { |r| r[:quote] }.uniq.sort

        _(quotes).must_equal(["AUD", "CHF", "EUR", "GBP", "JPY", "NZD", "USD", "XDR"])
      end

      it "relabels the SDR column as XDR" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 22), upto: Date.new(2026, 5, 22))
        xdr = dataset.find { |r| r[:quote] == "XDR" }

        _(xdr).wont_be_nil
        _(xdr[:rate]).must_be(:>, 0)
      end

      it "returns USD per FJD in a plausible range" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 22), upto: Date.new(2026, 5, 22))
        usd = dataset.find { |r| r[:quote] == "USD" }

        _(usd).wont_be_nil
        _(usd[:rate]).must_be(:>, 0.3)
        _(usd[:rate]).must_be(:<, 0.6)
      end

      it "respects the after and upto bounds" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 20), upto: Date.new(2026, 5, 21))
        dates = dataset.map { |r| r[:date] }.uniq.sort

        _(dates.min).must_be(:>=, Date.new(2026, 5, 20))
        _(dates.max).must_be(:<=, Date.new(2026, 5, 21))
      end

      it "raises when the XLSX link is missing from the hub page" do
        adapter.stub(:download, "<html><body>no link here</body></html>") do
          assert_raises(RuntimeError) { adapter.fetch }
        end
      end
    end
  end
end
