# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bbk"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BBK do
      before do
        VCR.insert_cassette("bbk", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { BBK.new }

      it "parses CSV with foreign base and DEM quote" do
        csv = <<~CSV
          DATAFLOW;BBK_STD_FREQ;BBK_STD_CURRENCY;BBK_ERX_PARTNER_CURRENCY;BBK_ERX_SERIES_TYPE;BBK_ERX_RATE_TYPE;BBK_ERX_SUFFIX;TIME_PERIOD;OBS_VALUE;TIME_FORMAT;BBK_DECIMALS;BBK_ID;BBK_UNIT;BBK_UNIT_MULT;BBK_TITLE;WEB_CATEGORY;BBK_COMM_GEN;BBK_DIFF;OBS_STATUS
          BBK:BBEX3(1.0);D;USD;DEM;AA;AC;000;1998-12-30;1.6730;P1D;4;BBEX3.D.USD.DEM.AA.AC.000;DEM;0;Devisenkurse der Frankfurter Börse / 1 USD = ... DEM / Vereinigte Staaten;WEDE;;0.0;
        CSV

        records = adapter.parse(csv)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("DEM")
        _(records.first[:rate]).must_equal(1.6730)
        _(records.first[:date]).must_equal(Date.new(1998, 12, 30))
      end

      it "scales rates using the hardcoded per-currency multiplier" do
        csv = <<~CSV
          DATAFLOW;BBK_STD_FREQ;BBK_STD_CURRENCY;BBK_ERX_PARTNER_CURRENCY;BBK_ERX_SERIES_TYPE;BBK_ERX_RATE_TYPE;BBK_ERX_SUFFIX;TIME_PERIOD;OBS_VALUE;TIME_FORMAT;BBK_DECIMALS;BBK_ID;BBK_UNIT;BBK_UNIT_MULT;BBK_TITLE;WEB_CATEGORY;BBK_COMM_GEN;BBK_DIFF;OBS_STATUS
          BBK:BBEX3(1.0);D;ATS;DEM;AA;AC;000;1998-12-30;14.214;P1D;3;BBEX3.D.ATS.DEM.AA.AC.000;DEM;0;Devisenkurse der Frankfurter Börse / 100 ATS = ... DEM / Österreich;WEDE;;0.0;
          BBK:BBEX3(1.0);D;ITL;DEM;AA;AC;000;1998-12-30;1.0100;P1D;4;BBEX3.D.ITL.DEM.AA.AC.000;DEM;0;Devisenkurse der Frankfurter Börse / 1 000 ITL = ... DEM / Italien;WEDE;;0.0;
        CSV

        records = adapter.parse(csv)
        ats = records.find { |r| r[:base] == "ATS" }
        itl = records.find { |r| r[:base] == "ITL" }

        _(ats[:rate]).must_be_close_to(0.14214, 0.00001)
        _(itl[:rate]).must_be_close_to(0.00101, 0.000001)
      end

      it "skips non-daily frequencies" do
        csv = <<~CSV
          DATAFLOW;BBK_STD_FREQ;BBK_STD_CURRENCY;BBK_ERX_PARTNER_CURRENCY;BBK_ERX_SERIES_TYPE;BBK_ERX_RATE_TYPE;BBK_ERX_SUFFIX;TIME_PERIOD;OBS_VALUE;TIME_FORMAT;BBK_DECIMALS;BBK_ID;BBK_UNIT;BBK_UNIT_MULT;BBK_TITLE;WEB_CATEGORY;BBK_COMM_GEN;BBK_DIFF;OBS_STATUS
          BBK:BBEX3(1.0);M;USD;DEM;AA;AC;A02;1998-12;1.6700;P1M;4;BBEX3.M.USD.DEM.AA.AC.A02;DEM;0;Devisenkurse der Frankfurter Börse / 1 USD = ... DEM / Vereinigte Staaten;WEDE;;0.0;
        CSV

        _(adapter.parse(csv)).must_be_empty
      end

      it "skips rows with missing OBS_VALUE" do
        csv = <<~CSV
          DATAFLOW;BBK_STD_FREQ;BBK_STD_CURRENCY;BBK_ERX_PARTNER_CURRENCY;BBK_ERX_SERIES_TYPE;BBK_ERX_RATE_TYPE;BBK_ERX_SUFFIX;TIME_PERIOD;OBS_VALUE;TIME_FORMAT;BBK_DECIMALS;BBK_ID;BBK_UNIT;BBK_UNIT_MULT;BBK_TITLE;WEB_CATEGORY;BBK_COMM_GEN;BBK_DIFF;OBS_STATUS
          BBK:BBEX3(1.0);D;USD;DEM;AA;AC;000;1998-12-24;.;P1D;4;BBEX3.D.USD.DEM.AA.AC.000;DEM;0;Devisenkurse der Frankfurter Börse / 1 USD = ... DEM / Vereinigte Staaten;WEDE;;;K
        CSV

        _(adapter.parse(csv)).must_be_empty
      end

      it "fetches rates for a historical date range" do
        dataset = adapter.fetch(after: Date.new(1998, 12, 21), upto: Date.new(1998, 12, 30))

        _(dataset).wont_be_empty
        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["DEM"])
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(1998, 12, 21), upto: Date.new(1998, 12, 30))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 5)
      end

      it "returns USD/DEM in a plausible range for late-1998" do
        dataset = adapter.fetch(after: Date.new(1998, 12, 29), upto: Date.new(1998, 12, 30))
        usd = dataset.find { |r| r[:base] == "USD" && r[:quote] == "DEM" && r[:date] == Date.new(1998, 12, 30) }

        _(usd).wont_be_nil
        _(usd[:rate]).must_be_close_to(1.67, 0.1)
      end
    end
  end
end
