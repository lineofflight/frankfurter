# frozen_string_literal: true

require_relative "../helper"
require "providers/nbu"

module Providers
  describe NBU do
    before do
      Rate.dataset.delete
    end

    let(:provider) { NBU.new }

    def count_unique_dates
      Rate.select(:date).distinct.count
    end

    describe "current" do
      before { VCR.insert_cassette("nbu_current", match_requests_on: [:method, :host]) }
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
      before { VCR.insert_cassette("nbu_historical", match_requests_on: [:method, :host]) }
      after { VCR.eject_cassette }

      it "imports historical rates" do
        provider.historical(start_date: "2026-03-01", end_date: "2026-03-16").import

        _(count_unique_dates).must_be(:>, 1)
      end
    end
  end
end
