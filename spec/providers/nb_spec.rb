# frozen_string_literal: true

require_relative "../helper"
require "providers/nb"

module Providers
  describe NB do
    before do
      Rate.dataset.delete
    end

    let(:provider) { NB.new }

    def count_unique_dates
      Rate.select(:date).distinct.count
    end

    it "fetches rates with date range" do
      VCR.use_cassette("nb", match_requests_on: [:method, :host]) do
        provider.fetch(since: Date.new(2026, 3, 16), upto: Date.new(2026, 3, 24)).import
      end

      _(count_unique_dates).must_be(:>=, 1)
    end

    it "stores multiple currencies per date" do
      VCR.use_cassette("nb", match_requests_on: [:method, :host]) do
        provider.fetch(since: Date.new(2026, 3, 16), upto: Date.new(2026, 3, 24)).import
      end
      date = Rate.first.date

      _(Rate.where(date:).count).must_be(:>, 1)
    end

    it "parses CSV with correct base and quote" do
      csv = <<~CSV
        FREQ,BASE_CUR,QUOTE_CUR,TENOR,TIME_PERIOD,OBS_VALUE,UNIT_MULT
        B,USD,NOK,SP,2026-03-16,10.5432,0
      CSV

      records = provider.parse(csv)

      _(records.length).must_equal(1)
      _(records.first[:base]).must_equal("USD")
      _(records.first[:quote]).must_equal("NOK")
      _(records.first[:rate]).must_equal(10.5432)
      _(records.first[:date]).must_equal(Date.new(2026, 3, 16))
    end

    it "adjusts rate by UNIT_MULT" do
      csv = <<~CSV
        FREQ,BASE_CUR,QUOTE_CUR,TENOR,TIME_PERIOD,OBS_VALUE,UNIT_MULT
        B,JPY,NOK,SP,2026-03-16,7.1234,2
      CSV

      records = provider.parse(csv)

      _(records.first[:rate]).must_be_close_to(0.071234, 0.000001)
    end

    it "filters non-business-day rows" do
      csv = <<~CSV
        FREQ,BASE_CUR,QUOTE_CUR,TENOR,TIME_PERIOD,OBS_VALUE,UNIT_MULT
        M,USD,NOK,SP,2026-03-16,10.5432,0
        B,EUR,NOK,SP,2026-03-16,11.2345,0
      CSV

      records = provider.parse(csv)

      _(records.length).must_equal(1)
      _(records.first[:base]).must_equal("EUR")
    end

    it "filters index codes on import" do
      csv = <<~CSV
        FREQ,BASE_CUR,QUOTE_CUR,TENOR,TIME_PERIOD,OBS_VALUE,UNIT_MULT
        B,I44,NOK,SP,2026-03-16,120.5432,0
        B,TWI,NOK,SP,2026-03-16,115.432,0
        B,USD,NOK,SP,2026-03-16,10.5432,0
      CSV

      # parse keeps all rows; import filters non-currencies via Money::Currency
      records = provider.parse(csv)

      _(records.length).must_equal(3)

      provider.instance_variable_set(:@dataset, records)
      provider.import

      _(Rate.where(provider: "NB").count).must_equal(1)
      _(Rate.first[:base]).must_equal("USD")
    end
  end
end
