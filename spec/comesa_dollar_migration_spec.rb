# frozen_string_literal: true

require_relative "helper"
require "open3"
require "tmpdir"

describe "COMESA Dollar migration" do
  it "promotes stored RBM CMD rates and leaves materializations for their existing repair lifecycle" do
    prepare = <<~RUBY
      require "db"
      require "bucket"
      Sequel.extension(:migration)
      Sequel::Migrator.run(DB, "db/migrate", target: 35)
      DB[:providers].insert(key: "RBM", name: "Reserve Bank of Malawi", pivot_currency: "MWK", frequency: "daily")

      date = Date.new(2024, 1, 2)
      week = DB.get(Bucket.week(date.to_s))
      month = DB.get(Bucket.month(date.to_s))
      DB[:rates].multi_insert([
        { provider: "RBM", date:, base: "CMD", quote: "MWK", mid: 100.0 },
        { provider: "RBM", date:, base: "USD", quote: "MWK", mid: 90.0 },
      ])
      DB[:currency_exclusions].insert(provider_key: "RBM", iso_code: "CMD", start_date: date, end_date: date)
      { weekly_rates: week, monthly_rates: month }.each do |table, bucket_date|
        DB[table].multi_insert([
          { provider: "RBM", bucket_date:, base: "CMD", quote: "MWK", rate: 100.0 },
          { provider: "RBM", bucket_date:, base: "USD", quote: "MWK", rate: 90.0 },
        ])
      end
      DB[:blended_rates].insert(date:, quote: "EUR", rate: 0.9)
      DB[:blended_weekly_rates].insert(bucket_date: week, quote: "EUR", rate: 0.9)
      DB[:blended_monthly_rates].insert(bucket_date: month, quote: "EUR", rate: 0.9)

      DB.disconnect
    RUBY

    repair = <<~RUBY
      require "db"
      require "rake"
      require "bucket"

      date = Date.new(2024, 1, 2)
      week = DB.get(Bucket.week(date.to_s))
      month = DB.get(Bucket.month(date.to_s))
      require "cache"
      Cache.define_singleton_method(:purge) { raise "Cloudflare unavailable" }
      load "lib/tasks/db.rake"
      Rake::Task["db:setup"].invoke
      Sequel::Migrator.check_current(DB, "db/migrate")

      # A second startup must also succeed while blends await the scheduler and the cache service is unavailable.
      Rake::Task.tasks.each(&:reenable)
      Rake::Task["db:setup"].invoke

      require "blended_rate"
      require "blended_weekly_rate"
      require "blended_monthly_rate"
      require "provider"
      abort "CMD still excluded" unless DB[:currency_exclusions].where(provider_key: "RBM", iso_code: "CMD").empty?
      abort "CMD missing coverage" unless DB[:currency_coverages].where(provider_key: "RBM", iso_code: "CMD").count == 1
      abort "CMD missing catalogue entry" unless DB[:currencies].where(iso_code: "CMD").count == 1
      abort "wrong CMD metadata" unless Money::Currency.find("CMD").name == "COMESA Dollar" && !Money::Currency.find("CMD").iso_numeric
      abort "CMD still fails provider health" if Provider["RBM"].unknown_currencies.include?("CMD")
      abort "daily blend stayed ready" if BlendedRate.ready?
      abort "daily blend stayed materialized" unless BlendedRate.empty?
      abort "weekly CMD bucket stayed materialized" unless BlendedWeeklyRate.where(bucket_date: week).empty?
      abort "monthly CMD bucket stayed materialized" unless BlendedMonthlyRate.where(bucket_date: month).empty?

      BlendedRate.rebuild
      [BlendedWeeklyRate, BlendedMonthlyRate].each(&:populate)
      abort "daily CMD blend missing" unless BlendedRate.where(date:, quote: "CMD").count == 1
      abort "weekly CMD blend missing" unless BlendedWeeklyRate.where(bucket_date: week, quote: "CMD").count == 1
      abort "monthly CMD blend missing" unless BlendedMonthlyRate.where(bucket_date: month, quote: "CMD").count == 1
      DB.disconnect
    RUBY

    Dir.mktmpdir do |dir|
      environment = {
        "DATABASE_URL" => "sqlite://#{File.join(dir, "migration.sqlite3")}",
        "APP_ENV" => "test",
      }
      output, status = Open3.capture2e(
        environment,
        RbConfig.ruby, "-Ilib", "-r./boot", "-e", prepare,
      )

      _(status.success?).must_equal(true, output)

      output, status = Open3.capture2e(
        environment,
        RbConfig.ruby, "-Ilib", "-r./boot", "-e", repair,
      )

      _(status.success?).must_equal(true, output)
    end
  end
end
