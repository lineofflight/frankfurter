# frozen_string_literal: true

require_relative "../helper"
require "providers/cba"

module Providers
  describe CBA do
    before do
      Rate.dataset.delete
      VCR.insert_cassette("cba")
    end

    after { VCR.eject_cassette }

    let(:provider) { CBA.new }

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
