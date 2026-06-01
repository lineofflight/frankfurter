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

      it "fetches rates quoted in PHP and USD" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 27), upto: Date.new(2026, 5, 29))

        _(dataset).wont_be_empty
        _(dataset.map { |r| r[:quote] }.uniq.sort).must_equal(["PHP", "USD"])
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
               BSP Buying Rate (T/T)PHP            61.350      GOLD BUYING:   $      4,495.00
               BSP Selling Rate (T/TPHP            61.850      SILVER BUYING: $         75.80
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

        it "emits the SDR rate as XDR over USD in its native direction" do
          records = adapter.parse_text(bulletin, Date.new(2026, 5, 29))
          xdr = records.find { |r| r[:base] == "XDR" }

          _(xdr).must_equal(date: Date.new(2026, 5, 29), base: "XDR", quote: "USD", rate: 1.36668)
        end

        it "emits gold as XAU per troy ounce over USD" do
          records = adapter.parse_text(bulletin, Date.new(2026, 5, 29))
          xau = records.find { |r| r[:base] == "XAU" }

          _(xau).must_equal(date: Date.new(2026, 5, 29), base: "XAU", quote: "USD", rate: 4495.0)
        end

        it "emits silver as XAG per troy ounce over USD" do
          records = adapter.parse_text(bulletin, Date.new(2026, 5, 29))
          xag = records.find { |r| r[:base] == "XAG" }

          _(xag).must_equal(date: Date.new(2026, 5, 29), base: "XAG", quote: "USD", rate: 75.80)
        end

        it "quotes XDR, XAU, and XAG in USD rather than PHP" do
          extras = ["XDR", "XAU", "XAG"]
          records = adapter.parse_text(bulletin, Date.new(2026, 5, 29))
          usd_denominated = records.select { |r| extras.include?(r[:base]) }

          _(usd_denominated.size).must_equal(3)
          _(usd_denominated.map { |r| r[:quote] }.uniq).must_equal(["USD"])
        end

        it "still emits the 30 PHP rows alongside the USD-denominated extras" do
          records = adapter.parse_text(bulletin, Date.new(2026, 5, 29))
          php_bases = records.select { |r| r[:quote] == "PHP" }.map { |r| r[:base] }

          _(php_bases).must_include("JPY")
          _(php_bases).must_include("EUR")
          _(php_bases).must_include("BHD")
          # USD comes from the BSP Reference Rate line, also PHP-quoted.
          _(php_bases).must_include("USD")
        end
      end
    end
  end
end
