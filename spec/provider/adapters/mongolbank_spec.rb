# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/mongolbank"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe Mongolbank do
      before do
        VCR.insert_cassette("mongolbank", match_requests_on: [:method, :host], allow_playback_repeats: true)
      end

      after do
        VCR.eject_cassette
      end

      let(:adapter) { Mongolbank.new }

      it "fetches rates" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 19), upto: Date.new(2026, 5, 22))

        _(dataset).wont_be_empty
      end

      it "respects after and upto filters" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 19), upto: Date.new(2026, 5, 22))
        dates = dataset.map { |r| r[:date] }.uniq

        _(dates.min).must_be(:>=, Date.new(2026, 5, 19))
        _(dates.max).must_be(:<=, Date.new(2026, 5, 22))
      end

      it "stores foreign currency as base and MNT as quote" do
        records = adapter.parse({
          "data" => [
            { "RATE_DATE" => "2026-05-22", "USD" => "3,576.42", "EUR" => "4,151.69" },
          ],
        })
        usd = records.find { |r| r[:base] == "USD" }

        _(usd[:base]).must_equal("USD")
        _(usd[:quote]).must_equal("MNT")
        _(usd[:rate]).must_equal(3576.42)
        _(usd[:date]).must_equal(Date.new(2026, 5, 22))
      end

      it "parses comma thousand-separators" do
        records = adapter.parse({
          "data" => [
            { "RATE_DATE" => "2026-05-22", "KWD" => "11,657.17" },
          ],
        })

        _(records.first[:rate]).must_equal(11657.17)
      end

      it "stores fractional rates for high-denomination currencies" do
        records = adapter.parse({
          "data" => [
            { "RATE_DATE" => "2026-05-22", "IDR" => "0.20", "VND" => "0.14", "KRW" => "2.36" },
          ],
        })
        rates = records.to_h { |r| [r[:base], r[:rate]] }

        _(rates["IDR"]).must_equal(0.20)
        _(rates["VND"]).must_equal(0.14)
        _(rates["KRW"]).must_equal(2.36)
      end

      it "excludes SDR composite" do
        records = adapter.parse({
          "data" => [
            { "RATE_DATE" => "2026-05-22", "USD" => "3,576.42", "SDR" => "4,887.93" },
          ],
        })
        bases = records.map { |r| r[:base] }

        _(bases).wont_include("SDR")
        _(bases).must_include("USD")
      end

      it "excludes KPW" do
        records = adapter.parse({
          "data" => [
            { "RATE_DATE" => "2026-05-22", "USD" => "3,576.42", "KPW" => "27.51" },
          ],
        })
        bases = records.map { |r| r[:base] }

        _(bases).wont_include("KPW")
      end

      it "stores XAU and XAG per troy ounce in MNT" do
        records = adapter.parse({
          "data" => [
            { "RATE_DATE" => "2026-05-22", "XAU" => "16,172,267.24", "XAG" => "271,833.67" },
          ],
        })
        xau = records.find { |r| r[:base] == "XAU" }
        xag = records.find { |r| r[:base] == "XAG" }

        _(xau[:quote]).must_equal("MNT")
        _(xau[:rate]).must_equal(16172267.24)
        _(xag[:rate]).must_equal(271833.67)
      end

      it "skips zero and empty values" do
        records = adapter.parse({
          "data" => [
            { "RATE_DATE" => "2026-05-22", "USD" => "3,576.42", "ZZZ" => "", "AAA" => "0.00" },
          ],
        })
        bases = records.map { |r| r[:base] }

        _(bases).must_equal(["USD"])
      end
    end
  end
end
