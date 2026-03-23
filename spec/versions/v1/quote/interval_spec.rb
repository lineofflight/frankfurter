# frozen_string_literal: true

require_relative "../../../helper"
require "versions/v1/quote/interval"

module Versions
  class V1 < Roda
    module Quote
      describe Interval do
        let(:dates) do
          (Fixtures.latest_date - 365)..Fixtures.latest_date
        end

        let(:quote) do
          Interval.new(date: dates)
        end

        before do
          quote.perform
        end

        it "returns rates" do
          _(quote.formatted[:rates]).wont_be(:empty?)
        end

        it "quotes given date interval" do
          returned_start = Date.parse(quote.formatted[:start_date])
          returned_end = Date.parse(quote.formatted[:end_date])

          # The returned start date should be the closest working day (before or on) the requested start
          _(returned_start).must_be(:<=, dates.first)
          # But it shouldn't be too far back (maybe 10 business days)
          _(returned_start).must_be(:>, dates.first - 10)

          # End date should still be within the requested range
          _(returned_end).must_be(:<=, dates.last)
        end

        it "quotes against the Euro" do
          quote.formatted[:rates].each_value do |rates|
            _(rates.keys).wont_include("EUR")
          end
        end

        it "sorts rates" do
          quote.formatted[:rates].each_value do |rates|
            _(rates.keys).must_equal(rates.keys.sort)
          end
        end

        it "has a cache key" do
          _(quote.cache_key).wont_be(:empty?)
        end

        describe "given a new base" do
          let(:quote) do
            Interval.new(date: dates, base: "USD")
          end

          it "quotes against that base" do
            quote.formatted[:rates].each_value do |rates|
              _(rates.keys).wont_include("USD")
            end
          end

          it "sorts rates" do
            quote.formatted[:rates].each_value do |rates|
              _(rates.keys).must_equal(rates.keys.sort)
            end
          end
        end

        describe "given symbols" do
          let(:quote) do
            Interval.new(date: dates, symbols: ["USD", "GBP", "JPY"])
          end

          it "quotes only for those symbols" do
            quote.formatted[:rates].each_value do |rates|
              _(rates.keys).must_include("USD")
              _(rates.keys).wont_include("CAD")
            end
          end

          it "sorts rates" do
            quote.formatted[:rates].each_value do |rates|
              _(rates.keys).must_equal(rates.keys.sort)
            end
          end
        end

        describe "when given an amount" do
          let(:quote) do
            Interval.new(date: dates, amount: 100)
          end

          it "calculates quotes for that amount" do
            quote.formatted[:rates].each_value do |rates|
              _(rates["USD"]).must_be(:>, 10)
            end
          end
        end
      end
    end
  end
end
