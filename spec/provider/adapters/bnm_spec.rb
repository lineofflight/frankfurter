# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bnm"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BNM do
      before do
        VCR.insert_cassette("bnm")
      end

      after do
        VCR.eject_cassette
      end

      let(:adapter) { BNM.new }

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

      it "normalizes rates by unit" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 31))
        jpy = dataset.find { |r| r[:base] == "JPY" }

        _(jpy).wont_be_nil
        _(jpy[:rate]).must_be(:<, 1)
      end

      it "fetches net-new currencies" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 31))
        bases = dataset.map { |r| r[:base] }.uniq

        ["BND", "EGP", "KHR", "MMK", "NPR"].each do |code|
          _(bases).must_include(code)
        end
      end

      it "respects upto date" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 10))

        _(dataset.any? { |r| r[:date] > Date.new(2026, 3, 10) }).must_equal(false)
        _(dataset.any? { |r| r[:date] <= Date.new(2026, 3, 10) }).must_equal(true)
      end
    end
  end
end
