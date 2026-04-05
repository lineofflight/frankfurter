# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/boc"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BOC do
      before do
        VCR.insert_cassette("boc", match_requests_on: [:method, :host])
      end

      after do
        VCR.eject_cassette
      end

      let(:adapter) { BOC.new }

      it "fetches rates" do
        dataset = adapter.fetch(after: Date.new(2025, 1, 1))

        _(dataset).wont_be_empty
      end

      it "stores foreign currency as base and CAD as quote" do
        records = adapter.parse({
          "observations" => [{
            "d" => "2026-03-20",
            "FXUSDCAD" => { "v" => "1.3728" },
          }],
        })

        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("CAD")
        _(records.first[:rate]).must_equal(1.3728)
      end
    end
  end
end
