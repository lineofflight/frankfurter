# frozen_string_literal: true

require_relative "../helper"
require "providers/cbr"

module Providers
  describe CBR do
    let(:provider) { CBR.new }

    def count_unique_dates
      Rate.select(:date).distinct.count
    end

    describe "current" do
      before do
        Rate.dataset.delete
        VCR.insert_cassette("cbr_current", allow_playback_repeats: true)
      end

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
      before do
        Rate.dataset.delete
        VCR.insert_cassette("cbr_historical", allow_playback_repeats: true)
      end

      after { VCR.eject_cassette }

      it "imports historical rates" do
        provider.historical(start_date: "2026-03-01", end_date: "2026-03-17").import

        _(count_unique_dates).must_be(:>, 1)
      end
    end
  end
end
