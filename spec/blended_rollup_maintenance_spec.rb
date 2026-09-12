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
    Rate.where(provider: "ECB", quote: "USD").update(rate: 1.3)
    invoke_task("rollups:rebuild", "ecb")

    [BlendedWeeklyRate, BlendedMonthlyRate].each do |model|
      stored = model.where(quote: "EUR").order(:bucket_date).last.rate
      model.rebuild

      _(model.where(quote: "EUR").order(:bucket_date).last.rate).must_equal(stored)
    end
  end

  it "invalidates grouped values in the same transaction as an invalid-data purge" do
    date = Date.today + 500
    Rate.dataset.insert(date:, provider: "ECB", base: "EUR", quote: "USD", rate: 1.2)
    Provider["ECB"].send(:refresh_rollups, [date])

    _(BlendedWeeklyRate.dataset.count).must_be(:>, 0)

    totals = RateValidation.purge(DB)

    _(totals[:rates]).must_be(:>, 0)
    _(BlendedWeeklyRate.dataset.count).must_equal(0)
    _(BlendedMonthlyRate.dataset.count).must_equal(0)
  end

  it "repopulates grouped tables after the purge task" do
    date = Date.today + 500
    Rate.dataset.insert(date:, provider: "ECB", base: "EUR", quote: "USD", rate: 1.2)
    Provider["ECB"].send(:refresh_rollups, [date])
    invoke_task("db:purge_invalid")

    _(BlendedWeeklyRate.ready?).must_equal(true)
    _(BlendedMonthlyRate.ready?).must_equal(true)
    _(BlendedWeeklyRate.max(:bucket_date)).must_be(:<, date.to_s)
  end
end
