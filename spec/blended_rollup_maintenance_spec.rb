# frozen_string_literal: true

require_relative "helper"
require "rake"
require "blended_weekly_rate"
require "blended_monthly_rate"
require "rate_validation"

["blend", "rollups", "db"].each { |name| load File.expand_path("../lib/tasks/#{name}.rake", __dir__) }

describe "Grouped blend maintenance" do
  def invoke_task(name, *)
    Rake::Task[name].reenable
    Cache.stub(:purge, nil) { Rake::Task[name].invoke(*) }
  end

  it "rebuilds all three materializations with blend:rebuild" do
    invoke_task("blend:rebuild")

    _(BlendedRate.ready?).must_equal(true)
    _(BlendedWeeklyRate.ready?).must_equal(true)
    _(BlendedMonthlyRate.ready?).must_equal(true)
  end

  it "refreshes grouped values when provider rollups are rebuilt" do
    BlendedWeeklyRate.rebuild
    BlendedMonthlyRate.rebuild
    Rate.where(provider: "ECB", quote: "USD").update(mid: 1.3)
    invoke_task("rollups:rebuild", "ecb")

    [BlendedWeeklyRate, BlendedMonthlyRate].each do |model|
      stored = model.where(quote: "EUR").order(:bucket_date).last.rate
      model.rebuild

      _(model.where(quote: "EUR").order(:bucket_date).last.rate).must_equal(stored)
    end
  end

  it "invalidates grouped values in the same transaction as an invalid-data purge" do
    date = Date.today + 500
    Rate.dataset.insert(date:, provider: "ECB", base: "EUR", quote: "USD", mid: 1.2)
    Provider["ECB"].send(:refresh_rollups, [date])

    _(BlendedWeeklyRate.dataset.count).must_be(:>, 0)
    [BlendedWeeklyRate, BlendedMonthlyRate].each(&:rebuild)
    before = BlendedWeeklyRate.where { bucket_date < Date.today }.count

    totals = RateValidation.purge(DB)

    _(totals[:rates]).must_be(:>, 0)
    _(BlendedWeeklyRate.where { bucket_date < Date.today }.count).must_equal(before)
    _(BlendedWeeklyRate.where { bucket_date > Date.today + 10 }.count).must_equal(0)
    _(BlendedMonthlyRate.where { bucket_date > Date.today + 10 }.count).must_equal(0)
  end

  it "repopulates grouped tables after the purge task" do
    date = Date.today + 500
    Rate.dataset.insert(date:, provider: "ECB", base: "EUR", quote: "USD", mid: 1.2)
    Provider["ECB"].send(:refresh_rollups, [date])
    invoke_task("db:purge_invalid")

    _(BlendedWeeklyRate.ready?).must_equal(true)
    _(BlendedMonthlyRate.ready?).must_equal(true)
    _(BlendedWeeklyRate.max(:bucket_date)).must_be(:<, date.to_s)
  end

  it "releases the source transaction before blending rebuilt provider buckets" do
    depth = 0
    observed = []
    transaction = DB.method(:transaction)
    refresh = BlendedWeeklyRate.method(:refresh)
    DB.stub(:transaction, lambda { |*args, **options, &block|
      depth += 1
      begin
        transaction.call(*args, **options, &block)
      ensure
        depth -= 1
      end
    },) do
      BlendedWeeklyRate.stub(:refresh, lambda { |dates|
        observed << depth
        refresh.call(dates)
      },) { invoke_task("rollups:rebuild", "ecb") }
    end

    _(observed).must_equal([0])
  end

  it "leaves changed buckets absent and unrelated history readable when maintenance refresh fails" do
    date = Date.new(2000, 1, 1)
    WeeklyRate.dataset.insert(bucket_date: date, provider: "BOC", base: "USD", quote: "EUR", rate: 0.8)
    [BlendedWeeklyRate, BlendedMonthlyRate].each(&:rebuild)
    prior = BlendedWeeklyRate.where(bucket_date: date).all.map(&:values)
    Rate.where(provider: "ECB", quote: "USD").update(mid: 1.3)

    BlendedWeeklyRate.stub(:refresh, ->(*) { raise "failed refresh" }) do
      _ { invoke_task("rollups:rebuild", "ecb") }.must_raise(RuntimeError)
    end

    _(WeeklyRate.where(provider: "ECB", quote: "USD").get(:rate)).must_equal(1.3)
    _(BlendedWeeklyRate.where(bucket_date: date).all.map(&:values)).must_equal(prior)
    _(BlendedWeeklyRate.where { bucket_date > date }.count).must_equal(0)
  end

  it "does not recompute grouped blends when rebuilding an excluded provider" do
    called = false
    BlendedWeeklyRate.stub(:refresh, ->(*) { called = true }) do
      invoke_task("rollups:rebuild", "ust")
    end

    _(called).must_equal(false)
  end

  it "repairs removed and replacement buckets without replacing unrelated provider history" do
    removed = Date.new(2000, 1, 1)
    kept = removed + 40
    [BlendedWeeklyRate, BlendedMonthlyRate].each do |model|
      model.source.dataset.insert(bucket_date: removed, provider: "ECB", base: "USD", quote: "EUR", rate: 0.8)
      model.source.dataset.insert(bucket_date: kept, provider: "BOC", base: "USD", quote: "EUR", rate: 0.9)
      model.rebuild
    end
    date = Date.new(2001, 3, 12)
    Rate.dataset.insert(date:, provider: "ECB", base: "USD", quote: "EUR", mid: 0.7)
    invoke_task("rollups:rebuild", "ecb")

    [BlendedWeeklyRate, BlendedMonthlyRate].each do |model|
      _(model.where(bucket_date: removed).count).must_equal(0)
      _(model.where(bucket_date: kept, quote: "EUR").get(:rate)).must_equal(0.9)
      expression = model == BlendedWeeklyRate ? Bucket.week(date.to_s) : Bucket.month(date.to_s)

      _(model.where(bucket_date: DB.get(expression), quote: "EUR").get(:rate)).must_equal(0.7)
    end
  end

  it "repairs purged grouped buckets and purges the cache even if the daily rebuild fails" do
    date = Fixtures.business_day(30)
    Rate.dataset.insert(date:, provider: "ECB", base: "USD", quote: "BYR", mid: 3.0)
    Provider["ECB"].send(:refresh_rollups, [date])
    [BlendedWeeklyRate, BlendedMonthlyRate].each(&:rebuild)
    purged = false
    Rake::Task["db:purge_invalid"].reenable
    BlendedRate.stub(:rebuild, -> { raise "daily rebuild failed" }) do
      Cache.stub(:purge, -> { purged = true }) do
        _ { Rake::Task["db:purge_invalid"].invoke }.must_raise(RuntimeError)
      end
    end

    _(BlendedWeeklyRate.ready?).must_equal(true)
    _(BlendedMonthlyRate.ready?).must_equal(true)
    _(BlendedWeeklyRate.where(quote: "BYR").count).must_equal(0)
    _(purged).must_equal(true)
  end

  it "recomputes a weekly average when a deleted daily observation shares a valid terminal bucket" do
    dates = [Date.new(2002, 2, 28), Date.new(2002, 3, 1)]
    rates = [15.0, 30.0]
    dates.zip(rates).each do |date, rate|
      Rate.dataset.insert(provider: "ECB", date:, base: "USD", quote: "ATS", mid: rate)
    end
    Provider["ECB"].send(:refresh_rollups, dates)
    bucket = DB.get(Bucket.week(dates.last.to_s))

    _(WeeklyRate.where(provider: "ECB", bucket_date: bucket, quote: "ATS").get(:rate)).must_equal(22.5)

    RateValidation.purge(DB)

    _(WeeklyRate.where(provider: "ECB", bucket_date: bucket, quote: "ATS").get(:rate)).must_equal(15.0)
    _(BlendedWeeklyRate.where(bucket_date: bucket).count).must_equal(0)
    BlendedWeeklyRate.populate

    _(BlendedWeeklyRate.where(bucket_date: bucket, quote: "ATS").get(:rate)).must_equal(15.0)
  end

  it "recomputes a monthly average when a daily observation crosses a mid-month terminal date" do
    dates = [Date.new(2018, 8, 19), Date.new(2018, 8, 20)]
    dates.zip([100.0, 300.0]).each do |date, rate|
      Rate.dataset.insert(provider: "ECB", date:, base: "USD", quote: "VEF", mid: rate)
    end
    Provider["ECB"].send(:refresh_rollups, dates)
    bucket = DB.get(Bucket.month(dates.last.to_s))

    _(MonthlyRate.where(provider: "ECB", bucket_date: bucket, quote: "VEF").get(:rate)).must_equal(200.0)

    RateValidation.purge(DB)

    _(MonthlyRate.where(provider: "ECB", bucket_date: bucket, quote: "VEF").get(:rate)).must_equal(100.0)
    _(BlendedMonthlyRate.where(bucket_date: bucket).count).must_equal(0)
  end

  it "removes stale source buckets when their last daily observation is purged" do
    date = Date.new(2002, 3, 1)
    Rate.dataset.insert(provider: "ECB", date:, base: "USD", quote: "ATS", mid: 30.0)
    Provider["ECB"].send(:refresh_rollups, [date])
    bucket = DB.get(Bucket.week(date.to_s))

    RateValidation.purge(DB)

    _(WeeklyRate.where(provider: "ECB", bucket_date: bucket).count).must_equal(0)
    _(BlendedWeeklyRate.where(bucket_date: bucket).count).must_equal(0)
  end

  it "rebuilds inception-straddling source buckets after coarse rollup purges" do
    dates = [Date.new(1999, 1, 1), Date.new(1999, 1, 4)]
    # The configured EUR inception is January 4; both observations share the January 1 monthly anchor.
    dates.zip([100.0, 2.0]).each do |date, rate|
      Rate.dataset.insert(provider: "ECB", date:, base: "USD", quote: "EUR", mid: rate)
    end
    Provider["ECB"].send(:refresh_rollups, dates)
    bucket = DB.get(Bucket.month(dates.last.to_s))

    RateValidation.purge(DB)

    _(Rate.where(provider: "ECB", quote: "EUR").select_map(:date)).must_equal([dates.last])
    _(MonthlyRate.where(provider: "ECB", bucket_date: bucket, quote: "EUR").get(:rate)).must_equal(2.0)

    RateValidation.purge(DB)

    _(MonthlyRate.where(provider: "ECB", bucket_date: bucket, quote: "EUR").get(:rate)).must_equal(2.0)
  end

  it "repairs excluded provider averages without invalidating the eligible grouped blend" do
    dates = [Date.new(2002, 2, 28), Date.new(2002, 3, 1)]
    Rate.dataset.insert(provider: "ECB", date: dates.first, base: "USD", quote: "EUR", mid: 0.8)
    Provider["ECB"].send(:refresh_rollups, [dates.first])
    dates.zip([15.0, 30.0]).each do |date, rate|
      Rate.dataset.insert(provider: "UST", date:, base: "USD", quote: "ATS", mid: rate)
    end
    Provider["UST"].send(:refresh_rollups, dates)
    bucket = DB.get(Bucket.week(dates.last.to_s))
    prior = BlendedWeeklyRate.where(bucket_date: bucket).order(:quote).all.map(&:values)

    RateValidation.purge(DB)

    _(WeeklyRate.where(provider: "UST", bucket_date: bucket, quote: "ATS").get(:rate)).must_equal(15.0)
    _(BlendedWeeklyRate.where(bucket_date: bucket).order(:quote).all.map(&:values)).must_equal(prior)
  end

  it "rolls back daily deletion and grouped invalidation if provider bucket repair fails" do
    dates = [Date.new(2002, 2, 28), Date.new(2002, 3, 1)]
    dates.zip([15.0, 30.0]).each do |date, rate|
      Rate.dataset.insert(provider: "ECB", date:, base: "USD", quote: "ATS", mid: rate)
    end
    Provider["ECB"].send(:refresh_rollups, dates)
    bucket = DB.get(Bucket.week(dates.last.to_s))
    prior = BlendedWeeklyRate.where(bucket_date: bucket).order(:quote).all.map(&:values)
    DB.run(<<~SQL)
      CREATE TEMP TRIGGER fail_rollup_repair BEFORE INSERT ON weekly_rates
      BEGIN SELECT RAISE(ABORT, 'failed provider bucket repair'); END
    SQL
    begin
      _ { RateValidation.purge(DB) }.must_raise(Sequel::DatabaseError)
    ensure
      DB.run("DROP TRIGGER fail_rollup_repair")
    end

    _(Rate.where(provider: "ECB", quote: "ATS").count).must_equal(2)
    _(WeeklyRate.where(provider: "ECB", bucket_date: bucket, quote: "ATS").get(:rate)).must_equal(22.5)
    _(BlendedWeeklyRate.where(bucket_date: bucket).order(:quote).all.map(&:values)).must_equal(prior)
  end
end
