# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/nbp"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe NBP do
      before do
        VCR.insert_cassette("nbp", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { NBP.new }

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

      it "includes Table B currencies" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1))
        bases = dataset.map { |r| r[:base] }.uniq

        _(bases).must_include("ALL")
      end
    end
  end
end
