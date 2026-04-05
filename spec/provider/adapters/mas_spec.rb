# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/mas"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe MAS do
      before do
        VCR.insert_cassette("mas", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { MAS.new }

      it "fetches rates" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 31))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 31))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "normalizes per-100-unit rates" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 31))
        jpy = dataset.find { |r| r[:base] == "JPY" }

        _(jpy).wont_be_nil
        _(jpy[:rate]).must_be(:<, 1)
      end

      it "includes per-unit currencies" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 31))
        eur = dataset.find { |r| r[:base] == "EUR" }

        _(eur).wont_be_nil
        _(eur[:rate]).must_be(:>, 1)
      end

      it "respects date boundaries" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 10))

        _(dataset.any? { |r| r[:date] > Date.new(2026, 3, 10) }).must_equal(false)
        _(dataset.any? { |r| r[:date] <= Date.new(2026, 3, 10) }).must_equal(true)
      end

      it "parses CSV data" do
        csv = <<~CSV
          MAS: Financial Database - Exchange Rates

          Exchange Rates (Daily)
          Mar 2026 to Mar 2026


          End of Period,,,S$ Per Unit of Euro,S$ Per Unit of US Dollar,S$ Per 100 Units of Japanese Yen,S$ Per 100 Units of Malaysian Ringgit
          2026,Mar,02,1.4940,1.2674,0.8107,32.45
          ,,03,1.4877,1.2726,0.8091,32.42
        CSV
        records = adapter.parse(csv)

        _(records.length).must_equal(8)
        eur = records.find { |r| r[:base] == "EUR" && r[:date] == Date.new(2026, 3, 2) }

        _(eur[:rate]).must_be_close_to(1.4940, 0.0001)
        _(eur[:quote]).must_equal("SGD")

        jpy = records.find { |r| r[:base] == "JPY" && r[:date] == Date.new(2026, 3, 2) }

        _(jpy[:rate]).must_be_close_to(0.008107, 0.00001)
      end

      it "skips empty values" do
        csv = <<~CSV
          End of Period,,,S$ Per Unit of Euro
          2026,Mar,02,
          ,,03,1.4877
        CSV
        records = adapter.parse(csv)

        _(records.length).must_equal(1)
      end

      it "handles empty CSV" do
        records = adapter.parse("")

        _(records).must_be_empty
      end
    end
  end
end
