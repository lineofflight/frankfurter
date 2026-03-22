# frozen_string_literal: true

require_relative "../helper"
require "providers/boc"

module Providers
  describe BOC do
    before do
      Rate.dataset.delete
      VCR.insert_cassette("boc", match_requests_on: [:method, :host])
    end

    after do
      VCR.eject_cassette
    end

    let(:provider) { BOC.new }

    def count_unique_dates
      Rate.select(:date).distinct.count
    end

    it "fetches rates" do
      provider.fetch(since: Date.new(2025, 1, 1)).import

      _(count_unique_dates).must_be(:>, 1)
    end

    it "fetches rates since a date" do
      provider.fetch(since: Date.today - 7).import

      _(count_unique_dates).must_be(:>=, 1)
    end

    it "stores multiple currencies per date" do
      provider.fetch(since: Date.today - 7).import
      date = Rate.first.date

      _(Rate.where(date:).count).must_be(:>, 1)
    end

    it "stores foreign currency as base and CAD as quote" do
      records = provider.parse({
        "observations" => [{
          "d" => "2026-03-20",
          "FXUSDCAD" => { "v" => "1.3728" },
        }],
      })

      _(records.first[:base]).must_equal("USD")
      _(records.first[:quote]).must_equal("CAD")
      _(records.first[:rate]).must_equal(1.3728)
    end
  end
end
