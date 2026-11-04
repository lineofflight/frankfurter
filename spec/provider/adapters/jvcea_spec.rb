# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/jvcea"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe JVCEA do
      before do
        VCR.insert_cassette("jvcea")
      end

      after { VCR.eject_cassette }

      let(:adapter) { JVCEA.new }

      it "fetches daily JPY reference rates for all six crypto assets" do
        dataset = adapter.fetch(after: Date.new(2026, 10, 20), upto: Date.new(2026, 10, 22))
        dates = dataset.map { |r| r[:date] }.uniq.sort
        bases = dataset.map { |r| r[:base] }.uniq.sort

        _(dates).must_equal((Date.new(2026, 10, 20)..Date.new(2026, 10, 22)).to_a)
        _(bases).must_equal(["BCH", "BTC", "ETH", "LTC", "SOL", "XRP"])
        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["JPY"])
      end

      it "reaches the 2020-05-01 archive start" do
        dataset = adapter.fetch(after: Date.new(2020, 5, 1), upto: Date.new(2020, 5, 2))
        first = dataset.find { |r| r[:base] == "BTC" && r[:date] == Date.new(2020, 5, 1) }

        _(first).wont_be_nil
        _(first[:rate]).must_be_close_to(962_124.31, 0.01)
      end

      it "skips days where the reference price is recorded as none" do
        dataset = adapter.fetch(after: Date.new(2026, 8, 15), upto: Date.new(2026, 8, 17))
        bch_dates = dataset.select { |r| r[:base] == "BCH" }.map { |r| r[:date] }.sort

        _(bch_dates).must_equal([Date.new(2026, 8, 15), Date.new(2026, 8, 17)])
      end

      it "flags the adapter as revising published values in place" do
        _(JVCEA.revises?).must_equal(true)
      end

      it "raises on a workbook without sheet1.xml" do
        buffer = Zip::OutputStream.write_buffer do |zip|
          zip.put_next_entry("xl/sharedStrings.xml")
          zip.write("<sst/>")
        end

        _ { adapter.parse(buffer.string) }.must_raise(RuntimeError)
      end
    end
  end
end
