# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/jpc"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe JPC do
      before { VCR.insert_cassette("jpc", match_requests_on: [:method, :uri]) }
      after { VCR.eject_cassette }

      let(:adapter) { JPC.new }

      it "fetches Sunday observations in the published foreign-to-JPY orientation" do
        rows = adapter.fetch(after: Date.new(2026, 9, 20), upto: Date.new(2026, 9, 27))

        _(rows.size).must_equal(168)
        _(rows.map { |r| r[:date] }.uniq).must_equal([Date.new(2026, 9, 20), Date.new(2026, 9, 27)])
        _(rows.all? { |r| r[:quote] == "JPY" }).must_equal(true)
        _(rows.find { |r| r[:date] == Date.new(2026, 9, 27) && r[:base] == "USD" }[:rate]).must_equal(155.39)
        _(rows.find { |r| r[:date] == Date.new(2026, 9, 27) && r[:base] == "KRW" }[:rate]).must_equal(0.1141)
        _(rows.none? { |r| r[:base] == "BND" }).must_equal(true)
      end

      it "includes a published coming week without fabricating future dates" do
        rows = Date.stub(:today, Date.new(2026, 9, 24)) do
          adapter.fetch(after: Date.new(2026, 9, 21))
        end

        _(rows.map { |r| r[:date] }.uniq).must_equal([Date.new(2026, 9, 27)])
        RateValidation.reject!(rows, lead_days: JPC.lead_days)

        _(rows.size).must_equal(84)
      end

      it "clips to effective start dates at both bounds" do
        rows = adapter.fetch(after: Date.new(2026, 9, 21), upto: Date.new(2026, 9, 26))

        _(rows).must_be_empty
      end

      it "reads the earliest archive despite legacy Japanese font encodings" do
        rows = adapter.fetch(after: Date.new(2002, 1, 1), upto: Date.new(2002, 1, 6))

        _(rows.size).must_equal(107)
        _(rows.map { |r| r[:date] }.uniq).must_equal([Date.new(2002, 1, 6)])
        _(rows.find { |r| r[:base] == "USD" }[:rate]).must_equal(130.97)
        _(rows.find { |r| r[:base] == "KRW" }[:rate]).must_equal(0.1003)
        _(rows.find { |r| r[:base] == "IDR" }[:rate]).must_equal(0.0129)
        _(rows.map { |r| r[:base] }).must_include("TRL")
        _(rows.find { |r| r[:base] == "RUB" }&.fetch(:rate)).must_equal(4.34)
        _(rows.find { |r| r[:base] == "YUM" }&.fetch(:rate)).must_equal(1.97)
      end

      it "identifies the redenominated zloty and lev behind stale archive labels" do
        rows = adapter.fetch(after: Date.new(2002, 1, 6), upto: Date.new(2002, 1, 6))

        _(rows.find { |r| r[:base] == "PLN" }&.fetch(:rate)).must_equal(BigDecimal("33.01"))
        _(rows.find { |r| r[:base] == "BGN" }&.fetch(:rate)).must_equal(BigDecimal("60.17"))
        _(rows.map { |r| r[:base] } & ["PLZ", "BGL"]).must_be_empty
      end

      it "crosses the archive index boundary without dropping a week" do
        rows = adapter.fetch(after: Date.new(2007, 12, 30), upto: Date.new(2008, 1, 6))

        _(rows.map { |r| r[:date] }.uniq).must_equal([Date.new(2007, 12, 30), Date.new(2008, 1, 6)])
        _(rows.find { |r| r[:date] == Date.new(2008, 1, 6) && r[:base] == "USD" }[:rate]).must_equal(114.13)
      end

      it "handles a week spanning two calendar years" do
        rows = adapter.fetch(after: Date.new(2019, 12, 29), upto: Date.new(2020, 1, 5))

        _(rows.map { |r| r[:date] }.uniq).must_equal([Date.new(2019, 12, 29), Date.new(2020, 1, 5)])
      end

      it "keeps weekly observations out of blends" do
        _(Provider["JPC"].blends?).must_equal(false)
      end

      describe "#parse_runs" do
        let(:date) { Date.new(2026, 9, 27) }

        def text_run(text, xpos, ypos = 100, width = 20)
          PDF::Reader::TextRun.new(xpos, ypos, width, 10, text)
        end

        it "uses numeric column positions to distinguish units and preserves decimal precision" do
          pages = [[text_run("USD", 360), text_run("155.39", 440),
                    text_run("KRW", 360, 80), text_run("11.41", 520, 79),
                    text_run("IDR", 360, 60), text_run("0.88", 520, 59),]]

          _(adapter.parse_runs(pages, date:)).must_equal([
            { date:, base: "USD", quote: "JPY", rate: BigDecimal("155.39") },
            { date:, base: "KRW", quote: "JPY", rate: BigDecimal("0.1141") },
            { date:, base: "IDR", quote: "JPY", rate: BigDecimal("0.0088") },
          ])
        end

        it "identifies the dinar behind the stale YUN label at the ISO transition" do
          pages = [[text_run("YUN", 360), text_run("1.80", 440),
                    text_run("KRW", 360, 80), text_run("9.86", 520, 79),]]

          before = adapter.parse_runs(pages, date: Date.new(2003, 1, 26))
          after = adapter.parse_runs(pages, date: Date.new(2003, 2, 2))

          _(before.first[:base]).must_equal("YUM")
          _(after.first[:base]).must_equal("CSD")
          _(after.first[:rate]).must_equal(1.80)
        end

        it "skips equivalence prose and zero rates" do
          pages = [[text_run("USD", 360), text_run("155.39", 440),
                    text_run("KRW", 360, 80), text_run("11.41", 520, 79),
                    text_run("BND", 360, 60), text_run("Equivalent to Singapore dollar", 420, 59),
                    text_run("XYZ", 360, 40), text_run("0.00", 440, 39),]]

          _(adapter.parse_runs(pages, date:).map { |r| r[:base] }).must_equal(["USD", "KRW"])
        end

        it "raises if a currency row has two numeric values" do
          pages = [[text_run("USD", 360), text_run("155.39", 440), text_run("15539.00", 520)]]

          _ { adapter.parse_runs(pages, date:) }.must_raise(RuntimeError)
        end

        it "rejects a numeric column outside the two published unit columns" do
          pages = [[text_run("USD", 360), text_run("155.39", 440),
                    text_run("KRW", 360, 80), text_run("11.41", 520, 79),
                    text_run("EUR", 360, 60), text_run("179.12", 480, 59),]]

          _ { adapter.parse_runs(pages, date:) }.must_raise(RuntimeError)
        end

        it "raises if the two unit columns cannot be identified" do
          _ { adapter.parse_runs([], date:) }.must_raise(RuntimeError)
          _ { adapter.parse_runs([[text_run("USD", 360), text_run("155.39", 440)]], date:) }.must_raise(RuntimeError)
        end
      end
    end
  end
end
