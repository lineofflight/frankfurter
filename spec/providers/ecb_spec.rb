# frozen_string_literal: true

require_relative "../helper"
require "providers/ecb"

module Providers
  describe ECB do
    before do
      Rate.dataset.delete
    end

    let(:provider) { ECB.new }

    def count_unique_dates
      Rate.select(:date).distinct.count
    end

    it "fetches historical rates" do
      VCR.use_cassette("ecb_historical", match_requests_on: [:method, :host]) do
        provider.fetch(since: Date.new(2025, 1, 1)).import
      end

      _(count_unique_dates).must_be(:>, 50)
    end
  end
end
