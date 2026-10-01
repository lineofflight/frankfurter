# frozen_string_literal: true

require_relative "helper"
require "open3"
require "tmpdir"

describe "BDI old afghani migration" do
  def run_migration_script(script)
    setup = <<~RUBY
      require "db"
      Sequel.extension(:migration)
      require "provider"
      require "blended_weekly_rate"
      require "blended_monthly_rate"
      Provider.seed
    RUBY
    Dir.mktmpdir do |dir|
      env = { "DATABASE_URL" => "sqlite://#{File.join(dir, "migration.sqlite3")}", "APP_ENV" => "test" }
      initialize = 'require "db"; Sequel.extension(:migration); Sequel::Migrator.run(DB, "db/migrate", target: 41)'
      output, status = Open3.capture2e(env, RbConfig.ruby, "-Ilib", "-r./boot", "-e", initialize)

      _(status.success?).must_equal(true, output)
      output, status = Open3.capture2e(env, RbConfig.ruby, "-Ilib", "-r./boot", "-e", setup + script)

      _(status.success?).must_equal(true, output)
    end
  end

  it "relabels BDI's AFN rows before its switch to AFA" do
    run_migration_script(<<~'RUBY')
      rows = [
        { provider: "BDI", date: "2002-10-04", base: "EUR", quote: "AFN", mid: 4685.87 },
        { provider: "BDI", date: "2002-10-04", base: "EUR", quote: "USD", mid: 0.9865 },
        { provider: "BDI", date: "2004-03-31", base: "EUR", quote: "AFN", mid: 5806.4 },
        { provider: "BDI", date: "2004-03-31", base: "EUR", quote: "AFA", mid: 5806.4 },
        { provider: "BDI", date: "2004-03-31", base: "EUR", quote: "USD", mid: 1.2224 },
        { provider: "BDI", date: "2004-04-01", base: "EUR", quote: "AFN", mid: 58.52 },
        { provider: "BDI", date: "2004-04-01", base: "EUR", quote: "USD", mid: 1.232 },
        { provider: "NBP", date: "2004-03-31", base: "AFN", quote: "PLN", mid: 0.0789 },
        { provider: "NBP", date: "2004-03-31", base: "USD", quote: "PLN", mid: 3.9077 },
        { provider: "ECB", date: "2026-01-20", base: "EUR", quote: "USD", mid: 1.17 },
      ]
      DB[:rates].multi_insert(rows)
      CurrencySummary.refresh(DB, ["AFA", "AFN", "EUR", "USD", "PLN"])
      [:weekly_rates, :monthly_rates].zip([Bucket.week, Bucket.month]).each do |table, bucket|
        DB[table].insert(
          [:bucket_date, :provider, :base, :quote, :rate],
          DB[:rates].select(bucket, :provider, :base, :quote, Sequel.function(:avg, :rate))
            .group(bucket, :provider, :base, :quote),
        )
      end
      [BlendedRate, BlendedWeeklyRate, BlendedMonthlyRate].each(&:rebuild)
      abort "fixture blend missing old AFN" if DB[:blended_rates].where(quote: "AFN", date: "2002-10-04").empty?
      affected_bucket = DB.get(Bucket.week("2002-10-04"))
      unaffected_bucket = DB.get(Bucket.week("2026-01-20"))
      unaffected = DB[:blended_weekly_rates].where(bucket_date: unaffected_bucket).all
      abort "missing unaffected fixture" if unaffected.empty?

      require "rake"
      load "lib/tasks/db.rake"
      Rake::Task["db:setup"].invoke
      abort "migration incomplete" unless DB[:schema_info].get(:version) >= 42

      stored = DB[:rates].where(provider: "BDI").exclude(quote: "USD").order(:date)
        .select_map([:date, :quote, :mid]).map { |date, quote, mid| [date.to_s, quote, mid] }
      expected = [["2002-10-04", "AFA", 4685.87], ["2004-03-31", "AFA", 5806.4], ["2004-04-01", "AFN", 58.52]]
      abort "wrong relabelled rows: #{stored.inspect}" unless stored == expected
      abort "touched other providers" unless DB[:rates].where(provider: "NBP", base: "AFN").count == 1

      weekly = DB[:weekly_rates].where(provider: "BDI").exclude(quote: "USD").order(:bucket_date, :quote)
        .select_map([:bucket_date, :quote]).map { |date, quote| [date.to_s, quote] }
      straddle = DB.get(Bucket.week("2004-03-31")).to_s
      expected = [[affected_bucket.to_s, "AFA"], [straddle, "AFA"], [straddle, "AFN"]]
      abort "wrong weekly rollups: #{weekly.inspect}" unless weekly == expected
      abort "AFN monthly rollup retained" unless DB[:monthly_rates].where(provider: "BDI", quote: "AFN")
        .select_map(:bucket_date).map(&:to_s) == [DB.get(Bucket.month("2004-04-01")).to_s]

      coverages = DB[:currency_coverages].where(iso_code: ["AFA", "AFN"]).order(:iso_code, :provider_key)
        .select_map([:iso_code, :provider_key, :start_date, :end_date]).map { |row| row.map(&:to_s) }
      expected = [
        ["AFA", "BDI", "2002-10-04", "2002-10-04"],
        ["AFN", "BDI", "2004-04-01", "2004-04-01"],
        ["AFN", "NBP", "2004-03-31", "2004-03-31"],
      ]
      abort "wrong coverage: #{coverages.inspect}" unless coverages == expected
      abort "AFN catalogue start" unless DB[:currencies].where(iso_code: "AFN").get(:start_date).to_s == "2004-03-31"
      abort "AFA catalogue" unless DB[:currencies].where(iso_code: "AFA").get(:start_date).to_s == "2002-10-04"

      abort "partial daily invalidation" unless DB[:blended_rates].empty?
      abort "kept affected bucket" unless DB[:blended_weekly_rates].where(bucket_date: affected_bucket).empty?
      abort "changed unrelated bucket" unless DB[:blended_weekly_rates].where(bucket_date: unaffected_bucket).all == unaffected

      BlendedRate.rebuild
      [BlendedWeeklyRate, BlendedMonthlyRate].each(&:populate)
      abort "old afghani blended as AFN" unless DB[:blended_rates].where(quote: "AFN").where { date < "2004-03-31" }.empty?
      abort "AFA missing from blend" if DB[:blended_rates].where(quote: "AFA", date: "2002-10-04").empty?
      abort "expired AFA blended" unless DB[:blended_rates].where(quote: "AFA").where { date >= "2002-10-07" }.empty?
      [BlendedRate, BlendedWeeklyRate, BlendedMonthlyRate].each do |model|
        abort "#{model} not ready" unless model.ready?
        recovered = model.dataset.order(*model.primary_key).all
        model.dataset.delete
        model.rebuild
        abort "rebuild mismatch for #{model}" unless model.dataset.order(*model.primary_key).all == recovered
      end
      DB.disconnect
    RUBY
  end

  it "rolls back on conflicting duplicates" do
    run_migration_script(<<~RUBY)
      DB[:rates].multi_insert([
        { provider: "BDI", date: "2004-03-31", base: "EUR", quote: "AFN", mid: 5806.4 },
        { provider: "BDI", date: "2004-03-31", base: "EUR", quote: "AFA", mid: 5806.5 },
      ])
      DB[:blended_rates].insert(date: "2004-03-31", quote: "AFN", rate: 4750.0)
      before = DB[:rates].order(:quote).all
      begin
        Sequel::Migrator.run(DB, "db/migrate")
        abort "accepted conflicting components"
      rescue RuntimeError => error
        raise unless error.message.include?("conflicting AFN/AFA components")
      end
      abort "partial repair committed" unless DB[:rates].order(:quote).all == before
      abort "migration marked complete" unless DB[:schema_info].get(:version) == 41
      abort "invalidated blends after failure" unless DB[:blended_rates].count == 1
      DB.disconnect
    RUBY
  end

  it "leaves databases without affected history alone" do
    run_migration_script(<<~RUBY)
      DB[:rates].insert(provider: "BDI", date: "2004-04-01", base: "EUR", quote: "AFN", mid: 58.52)
      DB[:blended_rates].insert(date: "2004-04-01", quote: "AFN", rate: 47.5)
      Sequel::Migrator.run(DB, "db/migrate")
      abort "unnecessary invalidation" unless DB[:blended_rates].count == 1
      abort "relabelled post-switch row" unless DB[:rates].get(:quote) == "AFN"
      DB.disconnect
    RUBY
  end
end
