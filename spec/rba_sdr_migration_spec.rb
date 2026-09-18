# frozen_string_literal: true

require_relative "helper"
require "open3"
require "tmpdir"

describe "RBA SDR migration" do
  def run_migration_script(script)
    setup = <<~RUBY
      require "db"
      Sequel.extension(:migration)
      require "provider"
      require "blended_weekly_rate"
      require "blended_monthly_rate"
      Provider.seed
      Cache.define_singleton_method(:purge) { raise IOError, "cache unavailable" }
    RUBY
    Dir.mktmpdir do |dir|
      env = { "DATABASE_URL" => "sqlite://#{File.join(dir, "migration.sqlite3")}", "APP_ENV" => "test" }
      initialize = 'require "db"; Sequel.extension(:migration); Sequel::Migrator.run(DB, "db/migrate", target: 37)'
      output, status = Open3.capture2e(env, RbConfig.ruby, "-Ilib", "-r./boot", "-e", initialize)

      _(status.success?).must_equal(true, output)
      output, status = Open3.capture2e(
        env,
        RbConfig.ruby, "-Ilib", "-r./boot", "-e", setup + script,
      )

      _(status.success?).must_equal(true, output)
    end
  end

  it "completes setup with an unavailable cache and preserves repaired history through recovery" do
    run_migration_script(<<~'RUBY')
      rows = [
        { provider: "RBA", date: "2026-01-01", base: "AUD", quote: "SDR", mid: 0.5131 },
        { provider: "RBA", date: "2026-01-02", base: "AUD", quote: "USD", mid: 0.6828 },
        { provider: "RBA", date: "2026-01-03", base: "AUD", quote: "SDR", mid: 0.5120 },
        { provider: "RBA", date: "2026-01-03", base: "AUD", quote: "XDR", mid: 0.5120 },
        { provider: "RBA", date: "2026-01-04", base: "AUD", quote: "XDR", mid: 0.5106 },
        { provider: "RBA", date: "2026-01-05", base: "SDR", quote: "AUD", mid: 1.95 },
        { provider: "RBA", date: "2026-01-06", base: "AUD", quote: "SDR", mid: nil, bid: 0.5100, ask: 0.5150 },
        { provider: "ECB", date: "2026-01-01", base: "USD", quote: "XDR", mid: 0.74 },
        { provider: "ECB", date: "2026-01-02", base: "USD", quote: "XDR", mid: 0.75 },
        { provider: "ECB", date: "2026-01-10", base: "USD", quote: "XDR", mid: 0.76 },
        { provider: "ECB", date: "2026-01-20", base: "USD", quote: "XDR", mid: 0.77 },
        { provider: "BOC", date: "2026-01-01", base: "SDR", quote: "CAD", mid: 1.8 },
      ]
      rows.each { |row| DB[:rates].insert(row) }
      raw = DB[:rates].order(:provider, :date, :base, :quote).all
      expected = raw.map do |row|
        row[:provider] == "RBA" ? row.transform_values { |value| value == "SDR" ? "XDR" : value } : row
      end.uniq.sort_by { |row| row.values_at(:provider, :date, :base, :quote) }
      CurrencySummary.refresh(DB, ["SDR", "XDR", "AUD", "USD", "CAD"])
      [:weekly_rates, :monthly_rates].zip([Bucket.week, Bucket.month]).each do |table, bucket|
        DB[table].insert(
          [:bucket_date, :provider, :base, :quote, :rate],
          DB[:rates].select(bucket, :provider, :base, :quote, Sequel.function(:avg, :rate))
            .group(bucket, :provider, :base, :quote),
        )
        DB[table].insert(bucket_date: "2025-12-01", provider: "RBA", base: "AUD", quote: "SDR", rate: 999)
      end
      [BlendedRate, BlendedWeeklyRate, BlendedMonthlyRate].each(&:rebuild)
      [BlendedWeeklyRate, BlendedMonthlyRate].each do |model|
        model.dataset.insert(bucket_date: "2025-12-01", quote: "EUR", rate: 999)
      end
      unaffected_bucket = DB[:rates].where(date: "2026-01-20").get(Bucket.week)
      unaffected = DB[:blended_weekly_rates].where(bucket_date: unaffected_bucket).all
      abort "missing unaffected fixture" if unaffected.empty?
      require "rake"
      load "lib/tasks/db.rake"
      Rake::Task["db:setup"].invoke
      abort "migration incomplete" unless DB[:schema_info].get(:version) >= 38
      abort "changed native observations" unless DB[:rates].order(:provider, :date, :base, :quote).all == expected
      abort "still unknown SDR" if Provider["RBA"].unknown_currencies.include?("SDR")
      abort "lost other provider exclusion" unless Provider["BOC"].unknown_currencies.include?("SDR")
      ["XDR", "AUD"].each do |code|
        coverage = DB[:currency_coverages].where(provider_key: "RBA", iso_code: code).first
        abort "wrong coverage for #{code}" unless coverage.values_at(:start_date, :end_date).map(&:to_s) == ["2026-01-01", "2026-01-06"]
        abort "missing catalogue #{code}" unless DB[:currencies].where(iso_code: code).first
      end
      abort "SDR entered catalogue" unless DB[:currencies].where(iso_code: "SDR").empty?
      abort "daily blend stayed ready" if BlendedRate.ready?
      abort "partial daily invalidation" unless DB[:blended_rates].empty?
      [BlendedWeeklyRate, BlendedMonthlyRate].each do |model|
        abort "grouped blend stayed ready" if model.ready?
        abort "orphan bucket retained" unless model.dataset.where(bucket_date: "2025-12-01").empty?
        abort "served affected bucket" if model.read(Date.new(2026, 1, 1)..Date.new(2026, 1, 6))
        abort "retained RBA SDR base rollup" unless model.source.where(provider: "RBA", base: "SDR").empty?
        abort "retained RBA SDR quote rollup" unless model.source.where(provider: "RBA", quote: "SDR").empty?
      end
      abort "changed unrelated bucket" unless DB[:blended_weekly_rates].where(bucket_date: unaffected_bucket).all == unaffected
      # Exercise the same recovery lifecycle as bin/schedule, then compare it with a clean full rebuild.
      BlendedRate.rebuild
      [BlendedWeeklyRate, BlendedMonthlyRate].each(&:populate)
      [BlendedRate, BlendedWeeklyRate, BlendedMonthlyRate].each do |model|
        abort "#{model} not ready" unless model.ready?
        recovered = model.dataset.order(*model.primary_key).all
        model.dataset.delete
        model.rebuild
        abort "rebuild mismatch for #{model}" unless model.dataset.order(*model.primary_key).all == recovered
      end
      # Rollups average the merged daily history rather than averaging old SDR/XDR averages.
      expected_average = DB[:rates].where(provider: "RBA", base: "AUD", quote: "XDR").avg(:rate)
      actual_average = DB[:monthly_rates].where(provider: "RBA", base: "AUD", quote: "XDR").get(:rate)
      abort "wrong merged rollup" unless actual_average == expected_average
      DB.disconnect
    RUBY
  end

  it "rolls back conflicting native components even when their effective rates match" do
    run_migration_script(<<~RUBY)
      DB[:rates].multi_insert([
        { provider: "RBA", date: "2026-01-01", base: "AUD", quote: "SDR", mid: 0.51, bid: nil, ask: nil },
        { provider: "RBA", date: "2026-01-02", base: "AUD", quote: "SDR", mid: 0.52, bid: nil, ask: nil },
        { provider: "RBA", date: "2026-01-02", base: "AUD", quote: "XDR", mid: nil, bid: 0.51, ask: 0.53 },
      ])
      DB[:blended_rates].insert(date: "2026-01-01", quote: "AUD", rate: 1.5)
      before = DB[:rates].order(:date, :base, :quote).all
      begin
        Sequel::Migrator.run(DB, "db/migrate")
        abort "accepted conflicting components"
      rescue RuntimeError => error
        raise unless error.message.include?("conflicting SDR/XDR components")
      end
      abort "partial repair committed" unless DB[:rates].order(:date, :base, :quote).all == before
      abort "migration marked complete" unless DB[:schema_info].get(:version) == 37
      abort "invalidated blends after failure" unless DB[:blended_rates].count == 1
      DB.disconnect
    RUBY
  end

  it "leaves databases without RBA SDR history alone" do
    run_migration_script(<<~RUBY)
      DB[:blended_rates].insert(date: "2026-01-01", quote: "AUD", rate: 1.5)
      Sequel::Migrator.run(DB, "db/migrate")
      abort "unnecessary invalidation" unless DB[:blended_rates].count == 1
      DB.disconnect
    RUBY
  end
end
