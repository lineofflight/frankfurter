# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/nbu"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe NBU do
      before do
        VCR.insert_cassette("nbu", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { NBU.new }

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
