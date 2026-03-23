# frozen_string_literal: true

require_relative "../helper"
require "providers/imf"

module Providers
  describe IMF do
    before do
      Rate.dataset.delete
      VCR.insert_cassette("imf", match_requests_on: [:method, :host])
    end

    after do
      VCR.eject_cassette
    end

    let(:provider) { IMF.new }

    def count_unique_dates
      Rate.select(:date).distinct.count
    end

    it "fetches rates" do
      provider.fetch(since: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 31)).import

      _(count_unique_dates).must_be(:>, 1)
    end

    it "stores multiple currencies per date" do
      provider.fetch(since: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 31)).import
      date = Rate.first.date

      _(Rate.where(date:).count).must_be(:>, 1)
    end

    it "parses indirect quotes with (1) suffix" do
      tsv = <<~TSV
        Representative Exchange Rates for Selected Currencies for January 2026
        Currency\tJanuary 02, 2026\tJanuary 05, 2026
        Euro(1)\t1.1698\t1.1606
        Japanese yen\t156.40\t157.41
        U.S. dollar\t1.0000\t1.0000
      TSV

      records = provider.parse(tsv)

      eur = records.select { |r| r[:base] == "EUR" || r[:quote] == "EUR" }

      _(eur.first[:base]).must_equal("EUR")
      _(eur.first[:quote]).must_equal("USD")

      jpy = records.select { |r| r[:base] == "USD" && r[:quote] == "JPY" }

      _(jpy.first[:rate]).must_equal(156.4)
    end

    it "skips USD rows" do
      tsv = <<~TSV
        Representative Exchange Rates for Selected Currencies for January 2026
        Currency\tJanuary 02, 2026
        U.S. dollar\t1.0000
        Euro(1)\t1.1698
      TSV

      records = provider.parse(tsv)

      _(records.none? { |r| r[:base] == "USD" && r[:quote] == "USD" }).must_equal(true)
    end
  end
end
