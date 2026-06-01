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

      it "fetches rates with PHP as the quote currency" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 27), upto: Date.new(2026, 5, 29))

        _(dataset).wont_be_empty
        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["PHP"])
      end

      it "covers the major quote currencies in the bulletin" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 29), upto: Date.new(2026, 5, 29))
        bases = dataset.map { |r| r[:base] }.uniq.sort

        _(bases).must_include("USD")
        _(bases).must_include("EUR")
        _(bases).must_include("JPY")
        _(bases).must_include("GBP")
      end

      it "emits the BSP Reference Rate for USD, not the row's Reuters equivalent" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 29), upto: Date.new(2026, 5, 29))
        usd = dataset.find { |r| r[:base] == "USD" }

        _(usd).wont_be_nil
        # BSP Reference Rate is 61.600; the USD row's peso equivalent is 61.6540.
        _(usd[:rate]).must_be_close_to(61.600, 0.001)
      end

      it "returns EUR/PHP in a plausible range" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 29), upto: Date.new(2026, 5, 29))
        eur = dataset.find { |r| r[:base] == "EUR" }

        _(eur).wont_be_nil
        _(eur[:rate]).must_be_close_to(71.84, 0.5)
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
             9 BAHRAIN                             DINAR*             BHD             2.276752     2.652872    163.5602
            10 KUWAIT                              DINAR              KWD                 N/A           N/A         N/A
            16 EUROPEAN MONETARY UNION             EURO               EUR             1.000000     1.165200      71.8392
               BSP Reference Rate:  PHP            61.600
               SDR Rate:            $               1.36668    /SDR
          TEXT
        end

        it "parses a normal row as foreign base over PHP quote" do
          records = adapter.parse_text(bulletin, Date.new(2026, 5, 29))
          jpy = records.find { |r| r[:base] == "JPY" }

          _(jpy).must_equal(date: Date.new(2026, 5, 29), base: "JPY", quote: "PHP", rate: 0.3872)
        end

        it "uses the symbol column even when the unit name carries an asterisk" do
          records = adapter.parse_text(bulletin, Date.new(2026, 5, 29))
          bhd = records.find { |r| r[:base] == "BHD" }

          _(bhd[:rate]).must_be_close_to(163.5602, 0.0001)
        end

        it "skips rows whose peso equivalent is N/A" do
          records = adapter.parse_text(bulletin, Date.new(2026, 5, 29))

          _(records.map { |r| r[:base] }).wont_include("KWD")
        end

        it "emits USD from the BSP Reference Rate line, not the row equivalent" do
          records = adapter.parse_text(bulletin, Date.new(2026, 5, 29))
          usd = records.find { |r| r[:base] == "USD" }

          _(usd[:rate]).must_be_close_to(61.600, 0.001)
        end

        it "does not emit the USD row's own Reuters peso equivalent" do
          records = adapter.parse_text(bulletin, Date.new(2026, 5, 29))
          usd_rates = records.select { |r| r[:base] == "USD" }.map { |r| r[:rate] }

          _(usd_rates).wont_include(61.6540)
        end

        it "skips the SDR line (USD-denominated, not PHP)" do
          records = adapter.parse_text(bulletin, Date.new(2026, 5, 29))

          _(records.map { |r| r[:base] }).wont_include("XDR")
          _(records.map { |r| r[:base] }).wont_include("SDR")
        end
      end
    end
  end
end
