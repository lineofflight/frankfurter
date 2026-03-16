# frozen_string_literal: true

require_relative "../helper"
require "providers/boc"

module Providers
  describe BOC do
    before do
      Rate.dataset.delete
      VCR.insert_cassette("boc")
    end

    after do
      VCR.eject_cassette
    end

    let(:provider) { BOC.new }

    def count_unique_dates
      Rate.select(:date).distinct.count
    end

    it "imports current rates" do
      provider.current.import

      _(count_unique_dates).must_equal(1)
    end

    it "imports historical rates" do
      provider.historical(start_date: "2025-01-01").import

      _(count_unique_dates).must_be(:>, 1)
    end

    it "stores multiple currencies per date" do
      provider.current.import
      date = Rate.first.date

      _(Rate.where(date:).count).must_be(:>, 1)
    end
  end
end
