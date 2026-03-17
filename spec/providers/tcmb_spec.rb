# frozen_string_literal: true

require_relative "../helper"
require "providers/tcmb"

module Providers
  describe TCMB do
    let(:provider) { TCMB.new }

    it "returns empty without API key" do
      original = ENV["TCMB_API_KEY"]
      ENV.delete("TCMB_API_KEY")

      _(provider.current.dataset).must_be(:empty?)
      _(provider.historical.dataset).must_be(:empty?)
    ensure
      ENV["TCMB_API_KEY"] = original
    end

    describe "with API key" do
      before do
        skip "TCMB_API_KEY not set" unless ENV["TCMB_API_KEY"]
        Rate.dataset.delete
      end

      def count_unique_dates
        Rate.select(:date).distinct.count
      end

      describe "current" do
        before { VCR.insert_cassette("tcmb_current", match_requests_on: [:method, :host]) }
        after { VCR.eject_cassette }

        it "imports" do
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
        before { VCR.insert_cassette("tcmb_historical") }
        after { VCR.eject_cassette }

        it "imports" do
          provider.historical(start_date: "2026-03-01", end_date: "2026-03-16").import

          _(count_unique_dates).must_be(:>, 1)
        end
      end
    end
  end
end
