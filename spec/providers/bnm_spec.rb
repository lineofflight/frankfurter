# frozen_string_literal: true

require_relative "../helper"
require "providers/bnm"

module Providers
  describe BNM do
    before do
      Rate.dataset.delete
      VCR.insert_cassette("bnm")
    end

    after do
      VCR.eject_cassette
    end

    let(:provider) { BNM.new }

    def count_unique_dates
      Rate.select(:date).distinct.count
    end

    it "fetches rates" do
      provider.fetch(since: Date.new(2026, 3, 1)).import

      _(count_unique_dates).must_be(:>, 1)
    end

    it "stores multiple currencies per date" do
      provider.fetch(since: Date.new(2026, 3, 1)).import
      date = Rate.first.date

      _(Rate.where(date:).count).must_be(:>, 1)
    end

    it "normalizes rates by unit" do
      provider.fetch(since: Date.new(2026, 3, 1)).import

      # JPY has unit=100, rate should be per single yen (small number)
      jpy_rate = Rate.where(quote: "JPY").first
      if jpy_rate
        _(jpy_rate.rate).must_be(:<, 1)
      end
    end

    it "excludes SDR" do
      provider.fetch(since: Date.new(2026, 3, 1)).import

      _(Rate.where(quote: "SDR").count).must_equal(0)
    end
  end
end
