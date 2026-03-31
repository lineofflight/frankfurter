# frozen_string_literal: true

require_relative "../helper"
require "providers/fred"

module Providers
  describe FRED do
    let(:provider) { FRED.new }

    it "requires an API key" do
      _(FRED.api_key?).must_equal(true)
    end

    describe "with API key" do
      before do
        skip "FRED_API_KEY not set" unless ENV["FRED_API_KEY"]
        Rate.dataset.delete
        VCR.insert_cassette("fred")
      end

      after { VCR.eject_cassette }

      def count_unique_dates
        Rate.select(:date).distinct.count
      end

      it "fetches rates since a date" do
        provider.fetch(since: Date.new(2026, 3, 1)).import

        _(count_unique_dates).must_be(:>, 1)
      end

      it "stores multiple currencies per date" do
        provider.fetch(since: Date.new(2026, 3, 1)).import
        date = Rate.first.date

        _(Rate.where(date:).count).must_be(:>, 1)
      end
    end
  end
end
