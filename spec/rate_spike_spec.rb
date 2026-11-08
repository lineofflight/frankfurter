# frozen_string_literal: true

require_relative "helper"
require "blended_rate"
require "blended_weekly_rate"
require "blended_monthly_rate"
require "rate_spike"
require "versions/v2/rate_query"

describe RateSpike do
  def daily(*values, from: Date.new(2024, 1, 15))
    values.each_with_index.map { |value, i| [from + i, value] }
  end

  def detect(series)
    Rate.dataset.multi_insert(series.map { |date, mid| { dataset_key: "T1", date:, base: "USD", quote: "SLE", mid: } })

    RateSpike.detect(DB[:rates].where(dataset_key: "T1")).select_map(:date).map(&:to_s).sort
  end

  describe ".detect" do
    it "flags a one-day typo above or below both neighbours" do
      _(detect(daily(3.26, 3.26, 43.26, 3.26, 0.326, 3.26))).must_equal(["2024-01-17", "2024-01-19"])
    end

    it "keeps a move short of the factor" do
      _(detect(daily(3.26, 9.7, 3.26))).must_be_empty
    end

    it "keeps a devaluation or redenomination that persists" do
      _(detect(daily(3.26, 32.6, 32.6))).must_be_empty
      _(detect(daily(3260.0, 3.26, 3.26, from: Date.new(2024, 2, 1)))).must_be_empty
    end

    it "keeps a value whose neighbours disagree" do
      # BDI's euro rate for the Zimbabwe dollar across the 2009 redenomination: no level to revert to.
      _(detect(daily(47_219_839.4304, 15_741_267_667.1, 28.2678))).must_be_empty
    end

    it "leaves the latest observation to blend until its successor arrives" do
      _(detect(daily(3.26, 3.26, 43.26))).must_be_empty
    end

    it "judges only against neighbours within the gap" do
      day = Date.new(2024, 1, 22)
      gap = RateSpike::MAX_GAP_DAYS

      _(detect([[day - gap, 3.26], [day, 43.26], [day + gap, 3.26]])).must_equal([day.to_s])

      Rate.dataset.delete

      _(detect([[day - gap - 1, 3.26], [day, 43.26], [day + gap, 3.26]])).must_be_empty
    end

    it "judges each pair against its own neighbours" do
      Rate.dataset.multi_insert(daily(43.26, 43.26, 43.26).map do |date, mid|
        { dataset_key: "T1", date:, base: "USD", quote: "GMD", mid: }
      end)

      _(detect(daily(3.26, 3.26, 3.26))).must_be_empty
    end
  end

  # The Central Bank of the Gambia published the leone at 43.26 dalasi on 2024-01-22, 3.26 either side. Three sources
  # quote the leone, too few for Consensus to screen an outlier.
  describe "the CBG leone typo" do
    let(:days) { [17, 18, 19, 22, 23, 24].map { |day| Date.new(2024, 1, day) } }
    let(:typo_day) { Date.new(2024, 1, 22) }
    let(:published) { ((70.0 / 43.26) + 21.5 + 21.6) / 3 }
    let(:screened) { ((70.0 / 3.26) + 21.5 + 21.6) / 3 }

    def cbg_rows(dates)
      dates.flat_map do |date|
        [
          { dataset_key: "CBG", date:, base: "USD", quote: "GMD", mid: 70.0 },
          { dataset_key: "CBG", date:, base: "SLE", quote: "GMD", mid: date == typo_day ? 43.26 : 3.26 },
        ]
      end
    end

    def other_rows
      days.flat_map do |date|
        [
          { dataset_key: "T1", date:, base: "USD", quote: "SLE", mid: 21.5 },
          { dataset_key: "T2", date:, base: "USD", quote: "SLE", mid: 21.6 },
        ]
      end
    end

    def roll_up(providers)
      [[:weekly_rates, Bucket.week], [:monthly_rates, Bucket.month]].each do |table, bucket|
        DB[table].insert(
          [:bucket_date, :dataset_key, :base, :quote, :rate],
          DB[:rates].where(dataset_key: providers, date: days)
            .select(bucket, :dataset_key, :base, :quote, Sequel.function(:avg, :rate))
            .group(bucket, :dataset_key, :base, :quote),
        )
      end
    end

    def bucket(model)
      DB.get(Bucket.expression(model == BlendedWeeklyRate ? :week : :month, typo_day.to_s))
    end

    def query(params, force_live: false)
      query = Versions::V2::RateQuery.new(params)
      query.force_live = force_live
      query.to_a
    end

    describe "once stored" do
      before do
        Rate.dataset.multi_insert(cbg_rows(days) + other_rows)
        roll_up(["CBG", "T1", "T2"])
        RateSpike.refresh("CBG", days)
      end

      it "screens the typo out of the daily blend and carries CBG's previous observation" do
        BlendedRate.rebuild

        _(RateSpike.dataset.select_map([:dataset_key, :base, :quote])).must_equal([["CBG", "SLE", "GMD"]])
        _(BlendedRate.first(quote: "SLE", date: typo_day).rate).must_be_within_delta(screened, 1e-9)

        row = query({ date: typo_day.to_s, base: "USD", quotes: "SLE", expand: "providers" }).first
        cbg = row[:providers].find { |p| p[:key] == "CBG" }

        _(cbg[:date]).must_equal("2024-01-19")
      end

      it "serves the published value to provider queries" do
        row = query({ date: typo_day.to_s, base: "SLE", quotes: "GMD", providers: "CBG" }).first

        _(row[:date]).must_equal(typo_day.to_s)
        _(row[:rate]).must_equal(43.26)
      end

      it "serves the same blend from the table and the live path" do
        BlendedRate.rebuild
        params = { from: days.first.to_s, to: days.last.to_s, base: "USD", quotes: "SLE" }

        _(query(params)).must_equal(query(params, force_live: true))
      end

      it "screens the typo out of weekly and monthly blends but not out of CBG's own averages" do
        [BlendedWeeklyRate, BlendedMonthlyRate].each do |model|
          model.rebuild
          cbg = model.source.where(dataset_key: "CBG", base: "SLE", bucket_date: bucket(model))

          _(cbg.get(:rate)).must_be(:>, 3.26)
          _(cbg.blendable.get(:rate)).must_be_within_delta(3.26, 1e-9)
          _(model.first(quote: "SLE", bucket_date: bucket(model)).rate).must_be_within_delta(screened, 1e-9)
        end
      end
    end

    describe "as CBG's latest observation" do
      let(:provider) { Provider["CBG"].dup }

      def adapter(dates)
        records = cbg_rows(dates).map { |row| row.except(:dataset_key, :mid).merge(rate: row[:mid]) }
        Class.new(Provider::Adapters::Adapter) do
          define_method(:fetch) { |after: nil, **| records.select { |r| after.nil? || r[:date] > after } }
        end
      end

      it "blends until the next observation arrives, then is screened and its blends refreshed" do
        Rate.dataset.multi_insert(other_rows)
        roll_up(["T1", "T2"])

        provider.stub(:adapter, adapter(days.select { |d| d <= typo_day })) { provider.backfill(after: days.first - 1) }

        _(RateSpike.dataset.count).must_equal(0)
        _(BlendedRate.first(quote: "SLE", date: typo_day).rate).must_be_within_delta(published, 1e-9)

        provider.stub(:adapter, adapter(days)) { provider.backfill(after: typo_day) }

        _(RateSpike.dataset.select_map(:date).map(&:to_s)).must_equal([typo_day.to_s])
        _(BlendedRate.first(quote: "SLE", date: typo_day).rate).must_be_within_delta(screened, 1e-9)
        [BlendedWeeklyRate, BlendedMonthlyRate].each do |model|
          _(model.first(quote: "SLE", bucket_date: bucket(model)).rate).must_be_within_delta(screened, 1e-9)
        end
      end

      it "is cleared when a late arrival turns the typo into a run" do
        Rate.dataset.multi_insert(cbg_rows(days - [days[4]]))
        RateSpike.refresh("CBG", days)

        _(RateSpike.dataset.count).must_equal(1)

        Rate.dataset.insert(dataset_key: "CBG", date: days[4], base: "SLE", quote: "GMD", mid: 43.26)

        _(RateSpike.refresh("CBG", [days[4]])).must_equal([typo_day])
        _(RateSpike.dataset.count).must_equal(0)
      end
    end
  end
  # A provider's default datasets roll up together, and a pair can move between them mid-bucket (RUB left NBP's Table A
  # for Table B on 2022-03-09). Screening has to read the pool as the one series its bare key serves.
  describe "across a dataset pool" do
    def nbp(key, date, mid)
      { dataset_key: key, date: Date.parse(date), base: "RUB", quote: "PLN", mid: }
    end

    it "keeps a spike stored under one table out of a rollup stored under the other" do
      rows = [
        nbp("NBP.A", "2025-03-03", 0.03), nbp("NBP.A", "2025-03-04", 0.30), nbp("NBP.A", "2025-03-05", 0.03),
        nbp("NBP.B", "2025-03-12", 0.03),
      ]
      Rate.dataset.multi_insert(rows)
      dates = rows.map { |row| row[:date] }
      RateSpike.refresh(["NBP.A", "NBP.B"], dates)
      Provider["NBP"].dup.send(:refresh_rollups, dates)

      _(RateSpike.dataset.select_map([:dataset_key, :date]).map { |key, date| [key, date.to_s] })
        .must_equal([["NBP.A", "2025-03-04"]])
      _(MonthlyRate.where(base: "RUB", bucket_date: "2025-03-01").select_map([:dataset_key, :rate]))
        .must_equal([["NBP.B", 0.0975]])
      _(MonthlyRate.blendable.where(base: "RUB", bucket_date: "2025-03-01").get(:rate)).must_be_close_to(0.03, 1e-12)
    end

    it "judges the last observation before a move against the first after it" do
      Rate.dataset.multi_insert([
        nbp("NBP.A", "2025-03-03", 0.03), nbp("NBP.A", "2025-03-04", 0.30), nbp("NBP.B", "2025-03-05", 0.03),
      ])

      _(RateSpike.detect(DB[:rates].where(dataset_key: ["NBP.A", "NBP.B"])).select_map([:dataset_key, :date])
        .map { |key, date| [key, date.to_s] }).must_equal([["NBP.A", "2025-03-04"]])
    end
  end
end
