# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/tcmb"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe TCMB do
      before do
        ENV["TCMB_API_KEY"] ||= "test"
        VCR.insert_cassette("tcmb", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { TCMB.new }
      let(:dataset) { adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 22)) }

      it "fetches rates since a date" do
        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "keeps every digit of the mid, whatever the magnitude" do
        # JPY is quoted per 100 units, so its rate lands three orders of magnitude below the rest of the feed. Rounding
        # to a fixed number of decimal places clipped it: buy 28.0072 and sell 28.1927 average to 28.09995, which is
        # 0.2809995 per yen, and a round(4) stored 0.281.
        record = dataset.find { |r| r[:base] == "JPY" && r[:date] == Date.new(2026, 3, 2) }

        _(record[:rate]).must_equal(0.2809995)
      end
    end
  end
end
