# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/nbrb"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe NBRB do
      before do
        VCR.insert_cassette("nbrb", match_requests_on: [:method, :uri])
      end

      after { VCR.eject_cassette }

      let(:adapter) { NBRB.new }

      it "fetches rates since a date" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 2), upto: Date.new(2026, 3, 6))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 2), upto: Date.new(2026, 3, 6))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "reaches back past the 2021 renumbering" do
        dataset = adapter.fetch(after: Date.new(2021, 7, 5), upto: Date.new(2021, 7, 13))
        usd = dataset.select { |r| r[:base] == "USD" }.to_h { |r| [r[:date], r[:rate]] }

        _(usd.keys.sort).must_equal(
          [5, 6, 7, 8, 9, 12, 13].map { |day| Date.new(2021, 7, day) },
        )
        _(usd[Date.new(2021, 7, 8)]).must_equal(2.5552)
        _(usd[Date.new(2021, 7, 9)]).must_equal(2.5921)
      end

      it "applies the scale of each ID" do
        dataset = adapter.fetch(after: Date.new(2021, 7, 5), upto: Date.new(2021, 7, 13))
        jpy = dataset.select { |r| r[:base] == "JPY" }.to_h { |r| [r[:date], r[:rate]] }

        _(jpy[Date.new(2021, 7, 8)]).must_be_close_to(0.023071, 1e-9)
        _(jpy[Date.new(2021, 7, 9)]).must_be_close_to(0.023585, 1e-9)
      end

      it "skips monthly IDs" do
        dataset = adapter.fetch(after: Date.new(2021, 7, 5), upto: Date.new(2021, 7, 13))
        amd = dataset.select { |r| r[:base] == "AMD" }.map { |r| r[:date] }

        _(amd.min).must_equal(Date.new(2021, 7, 9))
        _(dataset.map { |r| r[:base] }).wont_include("BRL")
      end

      it "starts at the BYN redenomination" do
        dataset = adapter.fetch(after: Date.new(2016, 6, 28), upto: Date.new(2016, 7, 5))
        usd = dataset.select { |r| r[:base] == "USD" }.to_h { |r| [r[:date], r[:rate]] }

        _(dataset.map { |r| r[:date] }.min).must_equal(Date.new(2016, 7, 1))
        _(usd[Date.new(2016, 7, 1)]).must_equal(2.0053)
      end
    end
  end
end
