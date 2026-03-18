# frozen_string_literal: true

require_relative "../helper"
require "providers/fred"

module Providers
  describe FRED do
    let(:provider) { FRED.new }

    it "returns empty without API key" do
      original = ENV["FRED_API_KEY"]
      ENV.delete("FRED_API_KEY")

      _(provider.current.dataset).must_be(:empty?)
      _(provider.historical.dataset).must_be(:empty?)
    ensure
      ENV["FRED_API_KEY"] = original
    end

    describe "with API key" do
      before do
        skip "FRED_API_KEY not set" unless ENV["FRED_API_KEY"]
        Rate.dataset.delete
      end

      def count_unique_dates
        Rate.select(:date).distinct.count
      end

      describe "current" do
        before { VCR.insert_cassette("fred_current") }
        after { VCR.eject_cassette }

        it "imports current rates" do
          provider.current.import

          _(count_unique_dates).must_equal(1)
        end

        it "stores multiple currencies per date" do
          provider.current.import
          date = Rate.first.date

          _(Rate.where(date:).count).must_be(:>, 1)
        end
      end

      describe "historical" do
        before { VCR.insert_cassette("fred_historical") }
        after { VCR.eject_cassette }

        it "imports historical rates" do
          provider.historical(start_date: "2026-03-01", end_date: "2026-03-14").import

          _(count_unique_dates).must_be(:>, 1)
        end
      end
    end
  end
end
