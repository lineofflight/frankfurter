# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bcrp"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BCRP do
      before { VCR.insert_cassette("bcrp") }
      after { VCR.eject_cassette }

      let(:adapter) { BCRP.new }
      let(:dataset) { adapter.fetch(after: Date.new(2026, 9, 1), upto: Date.new(2026, 9, 8)) }

      it "fetches rates since a date" do
        _(dataset).wont_be_empty
      end

      it "quotes USD in PEN" do
        _(dataset.map { |r| r[:base] }.uniq).must_equal(["USD"])
        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["PEN"])
      end

      it "skips days without data" do
        _(dataset.map { |r| r[:date] }).wont_include(Date.new(2026, 9, 8))
      end

      it "coerces buy and sell to an exact mid" do
        record = dataset.find { |r| r[:date] == Date.new(2026, 9, 4) }

        _(record[:rate]).must_equal(3.3645)
      end

      describe "#parse" do
        it "maps the Spanish September label" do
          records = adapter.parse('{"periods":[{"name":"04.Set.26","values":["3.36","3.369"]}]}')

          _(records.map { |r| r[:date] }).must_equal([Date.new(2026, 9, 4)])
        end

        it "parses two-digit years across the century" do
          records = adapter.parse('{"periods":[{"name":"02.Jan.97","values":["2.599","2.614"]}]}')

          _(records).must_equal([{ date: Date.new(1997, 1, 2), base: "USD", quote: "PEN", rate: 2.6065, bid: 2.599,
                                   ask: 2.614, mid: nil, }])
        end

        it "skips a pair missing either side" do
          records = adapter.parse('{"periods":[{"name":"25.Feb.02","values":["3.477","n.d."]}]}')

          _(records).must_be_empty
        end
      end
    end
  end
end
