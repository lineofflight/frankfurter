# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bis"
require "weekly_rate"
require "monthly_rate"
require "rack/test"
require "versions/v2"

class Provider
  module Adapters
    describe BIS do
      include Rack::Test::Methods

      let(:app) { Versions::V2.freeze }
      before { VCR.insert_cassette("bis", match_requests_on: [:method, :uri]) }
      after { VCR.eject_cassette }

      let(:adapter) { BIS.new }
      let(:header) { "FREQ,REF_AREA,CURRENCY,COLLECTION,TIME_PERIOD,OBS_VALUE,UNIT_MULT\n" }

      [["2001-05-31", "CDF", 104.4199881839], ["2021-09-30", "VES", 4128271.016399]].each do |day, quote, rate|
        it "preserves the published #{quote} digits through ingestion and the provider API" do
          date = Date.parse(day)
          records = adapter.fetch(after: date, upto: date).select { |r| r[:quote] == quote }
          batch = ->(**, &block) { block.call(records) }
          BIS.stub(:fetch_each, batch) { Provider["BIS"].backfill(after: date) }
          get "/rates", providers: "BIS", date: day, base: "USD", quotes: quote

          _(last_response).must_be(:ok?)
          _(JSON.parse(last_response.body).first.fetch("rate")).must_equal(rate)
          _(Rate.where(provider: "BIS", date:, quote:).get(:rate)).must_equal(rate)
        end
      end

      it "fetches actual month-end observations in the requested window" do
        records = adapter.fetch(after: Date.new(2025, 1, 31), upto: Date.new(2025, 2, 1))

        _(records.size).must_be(:>, 100)
        _(records.map { |r| r[:date] }.uniq).must_equal([Date.new(2025, 1, 31)])
        _(records.find { |r| r[:quote] == "JPY" }[:rate]).must_equal(154.902338)
        _(records.all? { |r| r[:base] == "USD" }).must_equal(true)
        _(records.map { |r| [r[:date], r[:quote]] }.uniq.size).must_equal(records.size)
      end

      it "backfills the earliest end-period observation" do
        records = adapter.fetch(after: Date.new(1900, 1, 1), upto: Date.new(1900, 1, 31))

        _(records).must_equal([{ date: Date.new(1900, 1, 31), base: "USD", quote: "ZAR", rate: 0.4107 }])
      end

      it "revisits the preceding year to import currencies published after the major-currency series" do
        source = [Date.new(2024, 1, 1), Date.new(2024, 12, 31), Date.new(2025, 1, 31)]
          .map { |date| { date:, base: "USD", quote: "JPY", rate: 150.0 } }
        window = ->(after:, upto:) { source.select { |row| row[:date].between?(after, upto || Date.today) } }
        records = []
        Date.stub(:today, Date.new(2025, 2, 1)) do
          BIS.stub(:new, adapter) do
            adapter.stub(:fetch, window) do
              BIS.fetch_each(after: Date.new(2025, 1, 31)) do |rows|
                records.concat(rows)
              end
            end
          end
        end

        _(records.map { |row| row[:date] }).must_equal([Date.new(2024, 12, 31), Date.new(2025, 1, 31)])
      end

      it "does not request dates before coverage or turn a future backfill into a historical request" do
        starts = []
        window = lambda do |after:, **|
          starts << after
          []
        end
        Date.stub(:today, Date.new(1900, 2, 1)) do
          BIS.stub(:new, adapter) do
            adapter.stub(:fetch, window) do
              BIS.fetch_each(after: Date.new(1900, 1, 31)) { |_| flunk }
              BIS.fetch_each(after: Date.new(1900, 2, 2)) { |_| flunk }
            end
          end
        end

        _(starts).must_equal([Date.new(1900, 1, 1)])
      end

      it "keeps BIS end-period observations out of daily and grouped blends" do
        Provider.seed
        provider = Provider["BIS"].dup
        [Rate, WeeklyRate, MonthlyRate].each do |model|
          field = model == Rate ? :mid : :rate
          model.dataset.insert(provider: "BIS", model.date_column => "2025-01-31", base: "USD", quote: "JPY",
                               field => 154.77,)

          _(model.where(provider: "BIS").count).must_equal(1)
          _(model.where(provider: "BIS").blendable.count).must_equal(0)
        end
        _(provider.lookback_days).must_equal(45)
        provider.define_singleton_method(:end_date) { "2025-01-31" }

        _(provider.publishes_missed(reference_date: Date.new(2025, 2, 6))).must_equal(0)
        _(provider.publishes_missed(reference_date: Date.new(2025, 3, 6))).must_equal(1)
      ensure
        Provider.load_cache
      end

      describe "#parse" do
        it "preserves published USD direction, precision, and restated historical units" do
          csv = "#{header}M,TR,TRY,E,1950-01,3E-06,0\n" \
                "M,TR,TRY,E,2025-01,35.672530181095,0\n"

          _(adapter.parse(csv)).must_equal([
            { date: Date.new(1950, 1, 31), base: "USD", quote: "TRY", rate: 0.000003 },
            { date: Date.new(2025, 1, 31), base: "USD", quote: "TRY", rate: 35.672530181095 },
          ])
        end

        it "selects canonical currency areas instead of synthetic national histories" do
          csv = "#{header}M,DE,EUR,E,1990-01,1.2,0\n" \
                "M,XM,EUR,E,1990-01,0.7,0\n" \
                "M,KI,AUD,E,1990-01,1.3,0\n" \
                "M,AU,AUD,E,1990-01,1.4,0\n" \
                "M,GW,XOF,E,1990-01,10,0\n" \
                "M,WA,XOF,E,1990-01,300,0\n" \
                "M,CF,XAF,E,1990-01,301,0\n" \
                "M,CM,XAF,E,1990-01,302,0\n" \
                "M,DM,XCD,E,1990-01,2.6,0\n" \
                "M,AG,XCD,E,1990-01,2.7,0\n" \
                "M,US,USD,E,1990-01,1,0\n"

          _(adapter.parse(csv).map { |r| [r[:quote], r[:rate]] })
            .must_equal([["XEU", 0.7], ["AUD", 1.4], ["XOF", 300.0], ["XAF", 302.0], ["XCD", 2.7]])
        end

        it "keeps the euro-area ECU history separate from EUR" do
          csv = "#{header}M,XM,EUR,E,1998-12,0.856824,0\nM,XM,EUR,E,1999-01,0.878426,0\n"

          _(adapter.parse(csv).map { |r| r[:quote] }).must_equal(["XEU", "EUR"])
        end

        it "corrects stale labels only when values are in successor units" do
          csv = "#{header}M,SL,SLL,E,2017-12,7.53696,0\n" \
                "M,SL,SLL,E,2022-07,13.88,0\n" \
                "M,VE,VEF,E,2018-07,172368,0\n" \
                "M,VE,VEF,E,2019-06,6550.047641,0\n" \
                "M,MR,MRO,E,2024-08,396,0\n" \
                "M,ST,STD,E,2026-06,21479.9,0\n"

          _(adapter.parse(csv).map { |r| [r[:quote], r[:rate]] }).must_equal([
            ["SLE", 7.53696], ["SLE", 13.88], ["VEF", 172368.0], ["VES", 6550.047641],
            ["MRO", 396.0], ["STD", 21479.9],
          ])
        end

        it "scales SDMX units and rejects missing, nonpositive, and non-end-period values" do
          csv = "#{header}M,JP,JPY,E,2024-02,1.5,2\n" \
                "M,JP,JPY,A,2024-02,140,0\n" \
                "D,JP,JPY,E,2024-02-29,150,0\n" \
                "M,CA,CAD,E,2024-02,NaN,0\n" \
                "M,GB,GBP,E,2024-02,,0\n" \
                "M,CH,CHF,E,2024-02,0,0\n" \
                "M,AU,AUD,E,2024-02,-1,0\n"

          _(adapter.parse(csv)).must_equal([{ date: Date.new(2024, 2, 29), base: "USD", quote: "JPY", rate: 150.0 }])
        end

        it "raises for unexpected content instead of silently losing data" do
          _ { adapter.parse("<html>Maintenance</html>") }.must_raise(RuntimeError)
          _ { adapter.parse("") }.must_raise(RuntimeError)
          _ { adapter.parse("#{header}M,JP,JPY,E,garbage,150,0\n") }.must_raise(Date::Error)
        end
      end
    end
  end
end
