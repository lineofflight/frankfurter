# frozen_string_literal: true

require_relative "../helper"
require "providers/bam"

module Providers
  describe BAM do
    before do
      Rate.dataset.delete
      VCR.insert_cassette("bam", match_requests_on: [:method, :host, :path])
    end

    after { VCR.eject_cassette }

    let(:provider) { BAM.new }

    def count_unique_dates
      Rate.select(:date).distinct.count
    end

    it "fetches rates with date range" do
      provider.fetch(since: Date.new(2026, 3, 25), upto: Date.new(2026, 3, 27)).import

      _(count_unique_dates).must_be(:>=, 1)
    end

    it "stores multiple currencies per date" do
      provider.fetch(since: Date.new(2026, 3, 25), upto: Date.new(2026, 3, 27)).import
      date = Rate.first.date

      _(Rate.where(date:).count).must_be(:>, 1)
    end

    it "parses records with correct base and quote" do
      json = <<~JSON
        [{"date": "2026-03-25T12:30:00", "libDevise": "USD", "moyen": 9.3794, "uniteDevise": 1}]
      JSON
      records = provider.parse(json)

      _(records.length).must_equal(1)
      _(records.first[:base]).must_equal("USD")
      _(records.first[:quote]).must_equal("MAD")
      _(records.first[:rate]).must_be_close_to(9.3794, 0.0001)
      _(records.first[:date]).must_equal(Date.new(2026, 3, 25))
    end

    it "normalizes rate by uniteDevise" do
      json = <<~JSON
        [{"date": "2026-03-25T12:30:00", "libDevise": "JPY", "moyen": 6.2500, "uniteDevise": 100}]
      JSON
      records = provider.parse(json)

      _(records.first[:rate]).must_be_close_to(0.0625, 0.0001)
    end

    it "skips zero rates" do
      json = <<~JSON
        [{"date": "2026-03-25T12:30:00", "libDevise": "USD", "moyen": 0.0, "uniteDevise": 1}]
      JSON
      records = provider.parse(json)

      _(records).must_be_empty
    end

    it "skips invalid currency codes" do
      json = <<~JSON
        [{"date": "2026-03-25T12:30:00", "libDevise": "XY", "moyen": 9.3794, "uniteDevise": 1}]
      JSON
      records = provider.parse(json)

      _(records).must_be_empty
    end

    it "handles empty response" do
      records = provider.parse("[]")

      _(records).must_be_empty
    end
  end
end
