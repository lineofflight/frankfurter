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

      it "skips the currency basket and other non-currency series" do
        csv = <<~CSV
          SERIES_CODE,FREQ,BASE_CURRENCY,COUNTER_CURRENCY,UNIT_MEASURE,DATA_TYPE,DATA_SOURCE,TIME_COLLECT,CONF_STATUS,PUB_WEBSITE,UNIT_MULT,COMMENTS,TIME_PERIOD,OBS_VALUE,RELEASE_STATUS
          RER_CBK_ILS,D,CBK_L,ILS,ILS,OF00,BOI_MRKT,V,F,Y,0,,2000-01-03,4.387,YP
          RER_USD_ILS,D,USD,ILS,ILS,OF00,BOI_MRKT,V,F,Y,0,,2000-01-03,4.124,YP
        CSV

        _(adapter.parse(csv).map { |r| r[:base] }).must_equal(["USD"])
      end

      it "normalizes pre-euro series published per 10, 100 or 1000 units" do
        csv = <<~CSV
          SERIES_CODE,FREQ,BASE_CURRENCY,COUNTER_CURRENCY,UNIT_MEASURE,DATA_TYPE,DATA_SOURCE,TIME_COLLECT,CONF_STATUS,PUB_WEBSITE,UNIT_MULT,COMMENTS,TIME_PERIOD,OBS_VALUE,RELEASE_STATUS
          RER_ATS_ILS,D,ATS,ILS,ILS,OF00,BOI_MRKT,V,F,Y,0,,2000-01-03,3.0234,YP
          RER_BEL_ILS,D,BEL,ILS,ILS,OF00,BOI_MRKT,V,F,Y,0,,2000-01-03,1.0313,YP
          RER_ESP_ILS,D,ESP,ILS,ILS,OF00,BOI_MRKT,V,F,Y,0,,2000-01-03,2.5004,YP
          RER_ITL_ILS,D,ITL,ILS,ILS,OF00,BOI_MRKT,V,F,Y,0,,2000-01-03,2.1486,YP
          RER_DEM_ILS,D,DEM,ILS,ILS,OF00,BOI_MRKT,V,F,Y,0,,2000-01-03,2.1271,YP
        CSV

        records = adapter.parse(csv).to_h { |r| [r[:base], r[:rate]] }

        _(records.keys).must_equal(["ATS", "BEF", "ESP", "ITL", "DEM"])
        _(records["ATS"]).must_be_close_to(0.30234, 1e-12)
        _(records["BEF"]).must_be_close_to(0.10313, 1e-12)
        _(records["ESP"]).must_be_close_to(0.025004, 1e-12)
        _(records["ITL"]).must_be_close_to(0.0021486, 1e-12)
        _(records["DEM"]).must_equal(2.1271)
      end
    end
  end
end
