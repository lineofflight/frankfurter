# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/cbr"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe CBR do
      before do
        VCR.insert_cassette("cbr", match_requests_on: [:method, :host], allow_playback_repeats: true)
      end

      after { VCR.eject_cassette }

      let(:adapter) { CBR.new }

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

      it "fetches foreign currency as base and RUB as quote" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1))
        usd = dataset.find { |r| r[:base] == "USD" && r[:quote] == "RUB" }

        _(usd).wont_be_nil
        _(usd[:rate]).must_be(:>, 50)
      end
    end
  end
end
