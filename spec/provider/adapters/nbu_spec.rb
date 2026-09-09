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

      it "restores the Tajikistani ruble code before the 2000 somoni" do
        json = [
          { "exchangedate" => "01.09.2000", "cc" => "TJS", "units" => 1000, "rate" => 2.7776 },
          { "exchangedate" => "02.12.2002", "cc" => "TJS", "units" => 1, "rate" => 1.805263 },
        ]

        records = adapter.parse(json)

        _(records.map { |r| r[:base] }).must_equal(["TJR", "TJS"])
        _(records.first[:rate]).must_be_close_to(0.0027776, 1e-9)
      end
    end
  end
end
