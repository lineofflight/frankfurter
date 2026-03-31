# frozen_string_literal: true

require_relative "../helper"
require "providers/boe"

module Providers
  describe BOE do
    before do
      Rate.dataset.delete
      VCR.insert_cassette("boe", match_requests_on: [:method, :host])
    end

    after { VCR.eject_cassette }

    let(:provider) { BOE.new }

    def count_unique_dates
      Rate.select(:date).distinct.count
    end

    it "fetches rates with date range" do
      provider.fetch(since: Date.new(2026, 3, 17), upto: Date.new(2026, 3, 20)).import

      _(count_unique_dates).must_be(:>=, 1)
    end

    it "stores multiple currencies per date" do
      provider.fetch(since: Date.new(2026, 3, 17), upto: Date.new(2026, 3, 20)).import
      date = Rate.first.date

      _(Rate.where(date:).count).must_be(:>, 1)
    end

    it "parses CSV with correct base and quote" do
      csv = <<~CSV
        DATE,XUDLUSS,XUDLERS
        17 Mar 2026,1.3343,1.1577
      CSV

      records = provider.parse(csv)

      usd = records.find { |r| r[:quote] == "USD" }
      eur = records.find { |r| r[:quote] == "EUR" }

      _(usd[:base]).must_equal("GBP")
      _(usd[:rate]).must_equal(1.3343)
      _(eur[:base]).must_equal("GBP")
      _(eur[:rate]).must_equal(1.1577)
      _(usd[:date]).must_equal(Date.new(2026, 3, 17))
    end

    it "skips empty values" do
      csv = <<~CSV
        DATE,XUDLUSS,XUDLZOS3
        17 Mar 2026,1.3343,
      CSV

      records = provider.parse(csv)

      _(records.length).must_equal(1)
      _(records.first[:quote]).must_equal("USD")
    end

    it "skips zero rates" do
      csv = <<~CSV
        DATE,XUDLUSS
        17 Mar 2026,0
      CSV

      records = provider.parse(csv)

      _(records).must_be_empty
    end
  end
end
