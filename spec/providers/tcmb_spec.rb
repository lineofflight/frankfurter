# frozen_string_literal: true

require_relative "../helper"
require "providers/tcmb"

module Providers
  describe TCMB do
    let(:provider) { TCMB.new }

    it "returns empty without API key" do
      original = ENV["TCMB_API_KEY"]
      ENV.delete("TCMB_API_KEY")

      _(provider.fetch.dataset).must_be(:empty?)
      _(provider.fetch(since: Date.today - 7).dataset).must_be(:empty?)
    ensure
      ENV["TCMB_API_KEY"] = original
    end

    describe "with API key" do
      before do
        skip "TCMB_API_KEY not set" unless ENV["TCMB_API_KEY"]
        Rate.dataset.delete
        VCR.insert_cassette("tcmb", match_requests_on: [:method, :host])
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
