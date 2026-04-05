# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bob"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BOB do
      before do
        VCR.insert_cassette("bob")
      end

      after do
        VCR.eject_cassette
      end

      let(:adapter) { BOB.new }

      it "fetches rates" do
        dataset = adapter.fetch

        _(dataset).wont_be_empty
      end

      it "fetches rates since a date" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end
    end
  end
end
