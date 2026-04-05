# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/nb"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe NB do
      let(:adapter) { NB.new }

      it "fetches rates with date range" do
        dataset = VCR.use_cassette("nb", match_requests_on: [:method, :host]) do
          adapter.fetch(after: Date.new(2026, 3, 16), upto: Date.new(2026, 3, 24))
        end

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = VCR.use_cassette("nb", match_requests_on: [:method, :host]) do
          adapter.fetch(after: Date.new(2026, 3, 16), upto: Date.new(2026, 3, 24))
        end
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "parses CSV with correct base and quote" do
        csv = <<~CSV
          FREQ,BASE_CUR,QUOTE_CUR,TENOR,TIME_PERIOD,OBS_VALUE,UNIT_MULT
          B,USD,NOK,SP,2026-03-16,10.5432,0
        CSV

        records = adapter.parse(csv)

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

        records = adapter.parse(csv)

        _(records.first[:rate]).must_be_close_to(0.071234, 0.000001)
      end

      it "filters non-business-day rows" do
        csv = <<~CSV
          FREQ,BASE_CUR,QUOTE_CUR,TENOR,TIME_PERIOD,OBS_VALUE,UNIT_MULT
          M,USD,NOK,SP,2026-03-16,10.5432,0
          B,EUR,NOK,SP,2026-03-16,11.2345,0
        CSV

        records = adapter.parse(csv)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("EUR")
      end

      it "parses index codes alongside currencies" do
        csv = <<~CSV
          FREQ,BASE_CUR,QUOTE_CUR,TENOR,TIME_PERIOD,OBS_VALUE,UNIT_MULT
          B,I44,NOK,SP,2026-03-16,120.5432,0
          B,TWI,NOK,SP,2026-03-16,115.432,0
          B,USD,NOK,SP,2026-03-16,10.5432,0
        CSV

        # parse keeps all rows; import filters non-currencies via Money::Currency
        records = adapter.parse(csv)

        _(records.length).must_equal(3)
      end
    end
  end
end
