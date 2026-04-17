# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bbk"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BBK do
      let(:adapter) { BBK.new }

      it "parses CSV with DEM base and foreign quote" do
        csv = <<~CSV
          KEY,FREQ,BBK_STD_CURRENCY,BBK_ERX_PARTNER_CURRENCY,BBK_ERX_SERIES_TYPE,BBK_ERX_RATE_TYPE,BBK_ERX_SUFFIX,TIME_PERIOD,OBS_VALUE
          BBEX3.D.USD.DEM.AA.AC.000,D,USD,DEM,AA,AC,000,1998-12-30,1.6730
        CSV

        records = adapter.parse(csv)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("DEM")
        _(records.first[:quote]).must_equal("USD")
        _(records.first[:rate]).must_equal(1.6730)
        _(records.first[:date]).must_equal(Date.new(1998, 12, 30))
      end

      it "skips rows with non-DEM partner currency" do
        csv = <<~CSV
          KEY,FREQ,BBK_STD_CURRENCY,BBK_ERX_PARTNER_CURRENCY,BBK_ERX_SERIES_TYPE,BBK_ERX_RATE_TYPE,BBK_ERX_SUFFIX,TIME_PERIOD,OBS_VALUE
          BBEX3.D.USD.EUR.AA.AC.000,D,USD,EUR,AA,AC,000,1998-12-30,1.1800
        CSV

        _(adapter.parse(csv)).must_be_empty
      end

      it "skips non-daily frequencies" do
        csv = <<~CSV
          KEY,FREQ,BBK_STD_CURRENCY,BBK_ERX_PARTNER_CURRENCY,BBK_ERX_SERIES_TYPE,BBK_ERX_RATE_TYPE,BBK_ERX_SUFFIX,TIME_PERIOD,OBS_VALUE
          BBEX3.M.USD.DEM.AA.AC.A02,M,USD,DEM,AA,AC,A02,1998-12,1.6700
        CSV

        _(adapter.parse(csv)).must_be_empty
      end

      it "skips rows with empty OBS_VALUE" do
        csv = <<~CSV
          KEY,FREQ,BBK_STD_CURRENCY,BBK_ERX_PARTNER_CURRENCY,BBK_ERX_SERIES_TYPE,BBK_ERX_RATE_TYPE,BBK_ERX_SUFFIX,TIME_PERIOD,OBS_VALUE
          BBEX3.D.USD.DEM.AA.AC.000,D,USD,DEM,AA,AC,000,1998-12-24,
        CSV

        _(adapter.parse(csv)).must_be_empty
      end
    end
  end
end
