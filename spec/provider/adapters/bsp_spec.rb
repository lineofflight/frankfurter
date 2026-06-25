# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bsp"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BSP do
      before do
        VCR.insert_cassette("bsp", match_requests_on: [:method, :uri])
      end

      after { VCR.eject_cassette }

      let(:adapter) { BSP.new }

      it "fetches only the BSP Reference Rate as USD over PHP" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 27), upto: Date.new(2026, 5, 29))

        _(dataset).wont_be_empty
        _(dataset.map { |r| [r[:base], r[:quote]] }.uniq).must_equal([["USD", "PHP"]])
      end

      it "emits the BSP Reference Rate, not the row's Reuters equivalent" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 29), upto: Date.new(2026, 5, 29))
        usd = dataset.find { |r| r[:base] == "USD" }

        _(usd).wont_be_nil
        # BSP Reference Rate is 61.600; the USD row's peso equivalent is 61.6540.
        _(usd[:rate]).must_be_close_to(61.600, 0.001)
      end

      it "filters records by the requested date range" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 27), upto: Date.new(2026, 5, 29))
        dates = dataset.map { |r| r[:date] }.uniq.sort

        _(dates.min).must_be(:>=, Date.new(2026, 5, 27))
        _(dates.max).must_be(:<=, Date.new(2026, 5, 29))
      end

      describe "#parse_text" do
        let(:bulletin) do
          <<~TEXT
             1 UNITED STATES                       DOLLAR             USD             0.858222     1.000000      61.6540
             2 JAPAN                               YEN                JPY             0.005390     0.006280       0.3872
            16 EUROPEAN MONETARY UNION             EURO               EUR             1.000000     1.165200      71.8392
               BSP Buying Rate (T/T)PHP            61.350      GOLD BUYING:   $      4,495.00
               BSP Selling Rate (T/TPHP            61.850      SILVER BUYING: $         75.80
               BSP Reference Rate:  PHP            61.600
               SDR Rate:            $               1.36668    /SDR
          TEXT
        end

        it "emits only the BSP Reference Rate as USD over PHP" do
          records = adapter.parse_text(bulletin, Date.new(2026, 5, 29))

          _(records).must_equal([{ date: Date.new(2026, 5, 29), base: "USD", quote: "PHP", rate: 61.600 }])
        end

        it "does not relay the LSEG currency table" do
          records = adapter.parse_text(bulletin, Date.new(2026, 5, 29))
          bases = records.map { |r| r[:base] }

          _(bases).wont_include("JPY")
          _(bases).wont_include("EUR")
        end

        it "does not relay the SDR or metals passthroughs" do
          records = adapter.parse_text(bulletin, Date.new(2026, 5, 29))
          bases = records.map { |r| r[:base] }

          _(bases).wont_include("XDR")
          _(bases).wont_include("XAU")
          _(bases).wont_include("XAG")
        end

        it "does not emit the USD row's own Reuters peso equivalent" do
          records = adapter.parse_text(bulletin, Date.new(2026, 5, 29))

          _(records.map { |r| r[:rate] }).wont_include(61.6540)
        end

        it "returns nothing when the Reference Rate line is absent" do
          records = adapter.parse_text("no reference rate in this text", Date.new(2026, 5, 29))

          _(records).must_be_empty
        end
      end
    end
  end
end
