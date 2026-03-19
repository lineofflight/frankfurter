# frozen_string_literal: true

require_relative "../helper"
require "providers/cbr"

module Providers
  describe CBR do
    before do
      Rate.dataset.delete
      VCR.insert_cassette("cbr", match_requests_on: [:method, :host], allow_playback_repeats: true)
    end

    after { VCR.eject_cassette }

    let(:provider) { CBR.new }

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
