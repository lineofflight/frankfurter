# frozen_string_literal: true

require_relative "../helper"
require "providers/cbk"

module Providers
  describe CBK do
    before do
      Rate.dataset.delete
      VCR.insert_cassette("cbk", match_requests_on: [:method, :host], allow_playback_repeats: true)
    end

    after do
      VCR.eject_cassette
    end

    let(:provider) { CBK.new }

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
      date = Rate.first&.date

      if date
        _(Rate.where(date:).count).must_be(:>, 1)
      end
    end

    it "stores foreign currency as base and KES as quote" do
      records = provider.parse({
        "data" => [
          ["20/03/2026", "US DOLLAR", "129.5012"],
        ],
      })

      _(records.first[:base]).must_equal("USD")
      _(records.first[:quote]).must_equal("KES")
      _(records.first[:rate]).must_equal(129.5012)
    end

    it "parses legacy table with mean rate" do
      records = provider.parse(
        "data" => [
          ["15/06/2020", "EURO", "85.1234", "84.0000", "86.0000"],
        ],
      )

      _(records.first[:base]).must_equal("EUR")
      _(records.first[:quote]).must_equal("KES")
      _(records.first[:rate]).must_equal(85.1234)
    end

    it "resolves East African cross-rate names" do
      records = provider.parse({
        "data" => [
          ["20/03/2026", "KEN SHILLING / USHS", "27.5000"],
          ["20/03/2026", "KEN SHILLING / TSHS", "20.1000"],
        ],
      })

      _(records.length).must_equal(2)
      _(records[0][:base]).must_equal("UGX")
      _(records[1][:base]).must_equal("TZS")
    end

    it "skips unmapped currencies" do
      records = provider.parse({
        "data" => [
          ["20/03/2026", "UNKNOWN CURRENCY", "1.0000"],
          ["20/03/2026", "US DOLLAR", "129.5012"],
        ],
      })

      _(records.length).must_equal(1)
      _(records.first[:base]).must_equal("USD")
    end
  end
end
