# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/fred"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe FRED do
      let(:adapter) { FRED.new }

      describe "with API key" do
        before do
          skip "FRED_API_KEY not set" unless ENV["FRED_API_KEY"]
          VCR.insert_cassette("fred")
        end

        after { VCR.eject_cassette }

        it "fetches rates since a date" do
          dataset = adapter.fetch(after: Date.new(2026, 3, 1))

          _(dataset).wont_be_empty
        end

        it "fetches multiple currencies per date" do
          dataset = adapter.fetch(after: Date.new(2026, 3, 1))
          dates = dataset.map { |r| r[:date] }.uniq
          sample = dataset.select { |r| r[:date] == dates.first }

          _(sample.size).must_be(:>, 1)
        end
      end
    end
  end
end
