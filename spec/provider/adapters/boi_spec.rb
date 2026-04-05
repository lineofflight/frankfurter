# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/boi"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BOI do
      before do
        VCR.insert_cassette("boi", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { BOI.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 20))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 20))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "parses CSV with correct base and quote" do
        csv = <<~CSV
          SERIES_CODE,FREQ,BASE_CURRENCY,COUNTER_CURRENCY,UNIT_MEASURE,DATA_TYPE,DATA_SOURCE,TIME_COLLECT,CONF_STATUS,PUB_WEBSITE,UNIT_MULT,COMMENTS,TIME_PERIOD,OBS_VALUE,RELEASE_STATUS
          RER_USD_ILS,D,USD,ILS,ILS,OF00,BOI_MRKT,V,F,Y,0,,2026-03-02,3.073,YP
        CSV

        records = adapter.parse(csv)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("ILS")
        _(records.first[:rate]).must_equal(3.073)
        _(records.first[:date]).must_equal(Date.new(2026, 3, 2))
      end

      it "adjusts rate by UNIT_MULT" do
        csv = <<~CSV
          SERIES_CODE,FREQ,BASE_CURRENCY,COUNTER_CURRENCY,UNIT_MEASURE,DATA_TYPE,DATA_SOURCE,TIME_COLLECT,CONF_STATUS,PUB_WEBSITE,UNIT_MULT,COMMENTS,TIME_PERIOD,OBS_VALUE,RELEASE_STATUS
          RER_JPY_ILS,D,JPY,ILS,ILS,OF00,BOI_MRKT,V,F,Y,2,,2026-03-02,1.971,YP
        CSV

        records = adapter.parse(csv)

        _(records.first[:rate]).must_be_close_to(0.01971, 0.00001)
      end

      it "handles LBP with UNIT_MULT 1" do
        csv = <<~CSV
          SERIES_CODE,FREQ,BASE_CURRENCY,COUNTER_CURRENCY,UNIT_MEASURE,DATA_TYPE,DATA_SOURCE,TIME_COLLECT,CONF_STATUS,PUB_WEBSITE,UNIT_MULT,COMMENTS,TIME_PERIOD,OBS_VALUE,RELEASE_STATUS
          RER_LBP_ILS,D,LBP,ILS,ILS,OF00,BOI_MRKT,V,F,Y,1,,2026-03-02,0.0003,YP
        CSV

        records = adapter.parse(csv)

        _(records.first[:rate]).must_be_close_to(0.00003, 0.000001)
      end
    end
  end
end
