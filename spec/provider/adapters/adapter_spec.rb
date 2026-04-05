# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/adapter"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe Adapter do
      it "requires fetch" do
        _ { Class.new(Adapter).new.fetch }.must_raise(NotImplementedError)
      end

      describe ".fetch_each" do
        it "yields records from fetch" do
          fetching_klass = Class.new(Adapter) do
            define_method(:fetch) do |**|
              [{ date: Date.new(2099, 1, 1), base: "EUR", quote: "USD", rate: 1.1 }]
            end
          end

          batches = []
          fetching_klass.fetch_each { |records| batches << records }

          _(batches.length).must_equal(1)
          _(batches[0].length).must_equal(1)
        end

        it "skips empty results" do
          empty_klass = Class.new(Adapter) do
            define_method(:fetch) do |**|
              []
            end
          end

          batches = []
          empty_klass.fetch_each { |records| batches << records }

          _(batches).must_be_empty
        end

        it "chunks by backfill_range" do
          after = Date.today - 90
          params = []
          chunked_klass = Class.new(Adapter) do
            class << self
              def backfill_range = 30
            end

            define_method(:fetch) do |after: nil, upto: nil|
              params << { after:, upto: }
              [{ date: Date.new(2099, 1, 1), base: "EUR", quote: "USD", rate: 1.1 }]
            end
          end

          chunked_klass.fetch_each(after:) { |_| }

          _(params.length).must_equal(4)
          _(params[0][:after]).must_equal(after)
          _(params[0][:upto]).must_equal(after + 29)
          _(params[-1][:upto]).must_be_nil
        end
      end
    end
  end
end
