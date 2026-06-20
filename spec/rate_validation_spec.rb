# frozen_string_literal: true

require_relative "helper"
require "rate_validation"

describe RateValidation do
  describe ".reject!" do
    it "drops unrecognised currency codes" do
      records = [
        { date: Date.today, base: "EUR", quote: "USD", rate: 1.1 },
        { date: Date.today, base: "EUR", quote: "SDR", rate: 1.5 },
      ]

      RateValidation.reject!(records)

      _(records.map { |r| r[:quote] }).must_equal(["USD"])
    end

    it "drops non-positive rates" do
      records = [
        { date: Date.today, base: "EUR", quote: "USD", rate: 1.1 },
        { date: Date.today, base: "EUR", quote: "GBP", rate: 0.0 },
        { date: Date.today, base: "EUR", quote: "JPY", rate: -1.0 },
      ]

      RateValidation.reject!(records)

      _(records.map { |r| r[:quote] }).must_equal(["USD"])
    end

    it "drops records beyond the future horizon but keeps the grace window" do
      records = [
        { date: Date.today + 1, base: "EUR", quote: "USD", rate: 1.1 },
        { date: Date.today + 365, base: "EUR", quote: "GBP", rate: 0.85 },
      ]

      RateValidation.reject!(records)

      _(records.map { |r| r[:quote] }).must_equal(["USD"])
    end

    it "drops records on or after a defunct currency's terminal date" do
      records = [
        { date: Date.new(2016, 7, 1), base: "EUR", quote: "BYR", rate: 22000.0 },
        { date: Date.new(2016, 6, 30), base: "EUR", quote: "BYR", rate: 22000.0 },
        { date: Date.new(2016, 7, 1), base: "EUR", quote: "USD", rate: 1.1 },
      ]

      RateValidation.reject!(records)

      _(records.size).must_equal(2)
      _(records.none? { |r| r[:quote] == "BYR" && r[:date] == Date.new(2016, 7, 1) }).must_equal(true)
    end

    it "drops EUR rates dated before the euro existed" do
      # The euro came into existence on 1999-01-04 (first ECB reference date). The Riksbank backfills its EUR series
      # with the ECU back to 1993; relaying those as EUR fabricates euro quotes for dates the euro did not exist.
      records = [
        { date: Date.new(1998, 12, 31), base: "SEK", quote: "EUR", rate: 0.10448 },
        { date: Date.new(1999, 1, 4), base: "SEK", quote: "EUR", rate: 0.10500 },
      ]

      RateValidation.reject!(records)

      _(records.map { |r| r[:date] }).must_equal([Date.new(1999, 1, 4)])
    end

    it "drops Austrian schilling rates on or after the euro changeover" do
      # ATS was irrevocably fixed to the euro in 1999 and ceased to be legal tender on 2002-02-28. Providers keep
      # publishing stale ATS reference rates years later (AMCM into 2004); they must be capped like IEP already is.
      records = [
        { date: Date.new(2002, 3, 1), base: "EUR", quote: "ATS", rate: 13.7603 },
        { date: Date.new(2002, 2, 28), base: "EUR", quote: "ATS", rate: 13.7603 },
      ]

      RateValidation.reject!(records)

      _(records.map { |r| r[:date] }).must_equal([Date.new(2002, 2, 28)])
    end

    it "accepts a string date" do
      records = [{ date: (Date.today - 1).to_s, base: "EUR", quote: "USD", rate: 1.1 }]

      RateValidation.reject!(records)

      _(records.size).must_equal(1)
    end
  end

  describe ".purge" do
    let(:db) { Sequel::Model.db }

    it "deletes future-dated rows across rate tables and keeps in-window rows" do
      future = Date.today + 365
      db[:rates].multi_insert([
        { provider: "TEST", date: future, base: "EUR", quote: "USD", rate: 1.1 },
        { provider: "TEST", date: Date.today, base: "EUR", quote: "USD", rate: 1.1 },
      ])
      db[:weekly_rates].insert(provider: "TEST", bucket_date: future, base: "EUR", quote: "USD", rate: 1.1)
      db[:monthly_rates].insert(provider: "TEST", bucket_date: future, base: "EUR", quote: "USD", rate: 1.1)

      RateValidation.purge(db)

      _(db[:rates].where(provider: "TEST", date: future).count).must_equal(0)
      _(db[:rates].where(provider: "TEST", date: Date.today).count).must_equal(1)
      _(db[:weekly_rates].where(provider: "TEST", bucket_date: future).count).must_equal(0)
      _(db[:monthly_rates].where(provider: "TEST", bucket_date: future).count).must_equal(0)
    end

    it "deletes rates on or after the terminal date and keeps earlier rows" do
      db[:rates].multi_insert([
        { provider: "TEST", date: Date.new(2016, 7, 1), base: "USD", quote: "BYR", rate: 22000.0 },
        { provider: "TEST", date: Date.new(2017, 1, 1), base: "BYR", quote: "USD", rate: 0.00005 },
        { provider: "TEST", date: Date.new(2016, 6, 30), base: "USD", quote: "BYR", rate: 22000.0 },
        { provider: "TEST", date: Date.new(2016, 7, 1), base: "EUR", quote: "USD", rate: 1.1 },
      ])

      totals = RateValidation.purge(db)

      _(totals[:rates]).must_equal(2)
      _(db[:rates].where(quote: "BYR", date: Date.new(2016, 7, 1)).count).must_equal(0)
      _(db[:rates].where(base: "BYR", date: Date.new(2017, 1, 1)).count).must_equal(0)
      _(db[:rates].where(quote: "BYR", date: Date.new(2016, 6, 30)).count).must_equal(1)
      _(db[:rates].where(base: "EUR", date: Date.new(2016, 7, 1)).count).must_equal(1)
    end

    it "deletes EUR rates dated before the euro existed and keeps later rows" do
      db[:rates].multi_insert([
        { provider: "TEST", date: Date.new(1998, 12, 31), base: "SEK", quote: "EUR", rate: 0.10448 },
        { provider: "TEST", date: Date.new(1999, 1, 4), base: "SEK", quote: "EUR", rate: 0.10500 },
      ])

      RateValidation.purge(db)

      _(db[:rates].where(provider: "TEST", quote: "EUR", date: Date.new(1998, 12, 31)).count).must_equal(0)
      _(db[:rates].where(provider: "TEST", quote: "EUR", date: Date.new(1999, 1, 4)).count).must_equal(1)
    end

    it "removes rollup rows past the terminal date" do
      db[:weekly_rates].multi_insert([
        { provider: "TEST", bucket_date: Date.new(2016, 7, 4), base: "USD", quote: "BYR", rate: 22000.0 },
        { provider: "TEST", bucket_date: Date.new(2016, 6, 27), base: "USD", quote: "BYR", rate: 22000.0 },
      ])
      db[:monthly_rates].multi_insert([
        { provider: "TEST", bucket_date: Date.new(2016, 8, 1), base: "USD", quote: "BYR", rate: 22000.0 },
        { provider: "TEST", bucket_date: Date.new(2016, 6, 1), base: "USD", quote: "BYR", rate: 22000.0 },
      ])

      totals = RateValidation.purge(db)

      _(totals[:weekly_rates]).must_equal(1)
      _(totals[:monthly_rates]).must_equal(1)
      _(db[:weekly_rates].where(quote: "BYR", bucket_date: Date.new(2016, 6, 27)).count).must_equal(1)
      _(db[:monthly_rates].where(quote: "BYR", bucket_date: Date.new(2016, 6, 1)).count).must_equal(1)
    end

    it "keeps the current period's rollup whose bucket anchor sits past the daily horizon" do
      # Weekly/monthly buckets anchor to a fixed weekday / first of the month, so the live period's bucket can sit a few
      # days ahead of the latest date it summarises. The daily future horizon must not purge it.
      RateValidation::FutureDate.stub(:horizon, Date.new(2026, 6, 15)) do # a Monday
        db[:weekly_rates].multi_insert([
          { provider: "TEST", bucket_date: Date.new(2026, 6, 18), base: "EUR", quote: "USD", rate: 1.1 }, # current week
          { provider: "TEST", bucket_date: Date.new(2026, 6, 25), base: "EUR", quote: "USD", rate: 1.1 }, # next week
        ])
        db[:monthly_rates].multi_insert([
          { provider: "TEST", bucket_date: Date.new(2026, 6, 1), base: "EUR", quote: "USD", rate: 1.1 }, # current month
          { provider: "TEST", bucket_date: Date.new(2026, 7, 1), base: "EUR", quote: "USD", rate: 1.1 }, # next month
        ])

        RateValidation.purge(db)

        _(db[:weekly_rates].where(provider: "TEST", bucket_date: Date.new(2026, 6, 18)).count).must_equal(1)
        _(db[:weekly_rates].where(provider: "TEST", bucket_date: Date.new(2026, 6, 25)).count).must_equal(0)
        _(db[:monthly_rates].where(provider: "TEST", bucket_date: Date.new(2026, 6, 1)).count).must_equal(1)
        _(db[:monthly_rates].where(provider: "TEST", bucket_date: Date.new(2026, 7, 1)).count).must_equal(0)
      end
    end

    it "refreshes currency summaries for affected codes" do
      db[:rates].multi_insert([
        { provider: "TEST", date: Date.new(2016, 6, 30), base: "USD", quote: "BYR", rate: 22000.0 },
        { provider: "TEST", date: Date.new(2017, 1, 1), base: "USD", quote: "BYR", rate: 22000.0 },
      ])
      db[:currencies].where(iso_code: "BYR").delete
      db[:currencies].insert(iso_code: "BYR", start_date: "2016-06-30", end_date: "2017-01-01")
      db[:currency_coverages].where(iso_code: "BYR").delete
      db[:currency_coverages].insert(
        provider_key: "TEST",
        iso_code: "BYR",
        start_date: "2016-06-30",
        end_date: "2017-01-01",
      )

      RateValidation.purge(db)

      currency = db[:currencies].where(iso_code: "BYR").first
      coverage = db[:currency_coverages].where(iso_code: "BYR", provider_key: "TEST").first

      _(currency[:end_date].to_s).must_equal("2016-06-30")
      _(coverage[:end_date].to_s).must_equal("2016-06-30")
    end

    it "removes currency rows entirely when no surviving rates remain" do
      db[:rates].insert(
        provider: "TEST",
        date: Date.new(2017, 1, 1),
        base: "USD",
        quote: "BYR",
        rate: 22000.0,
      )
      db[:currencies].where(iso_code: "BYR").delete
      db[:currencies].insert(iso_code: "BYR", start_date: "2017-01-01", end_date: "2017-01-01")
      db[:currency_coverages].where(iso_code: "BYR").delete
      db[:currency_coverages].insert(
        provider_key: "TEST",
        iso_code: "BYR",
        start_date: "2017-01-01",
        end_date: "2017-01-01",
      )

      RateValidation.purge(db)

      _(db[:currencies].where(iso_code: "BYR").count).must_equal(0)
      _(db[:currency_coverages].where(iso_code: "BYR").count).must_equal(0)
    end
  end
end
