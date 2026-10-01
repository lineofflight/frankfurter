# frozen_string_literal: true

require_relative "helper"
require "open3"
require "tmpdir"

describe "Belarusian rubles migration" do
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
      initialize = 'require "db"; Sequel.extension(:migration); Sequel::Migrator.run(DB, "db/migrate", target: 46)'
      output, status = Open3.capture2e(env, RbConfig.ruby, "-Ilib", "-r./boot", "-e", initialize)

      _(status.success?).must_equal(true, output)
      output, status = Open3.capture2e(env, RbConfig.ruby, "-Ilib", "-r./boot", "-e", setup + script)

      _(status.success?).must_equal(true, output)
    end
  end

  it "relabels stored rows and leaves the blends to a rebuild" do
    run_migration_script(<<~'RUBY')
      rows = [
        { provider: "NBK", date: "2004-09-14", base: "BYN", quote: "KZT", mid: 0.06 },
        { provider: "NBK", date: "2016-07-01", base: "BYN", quote: "KZT", mid: 0.0168 },
        { provider: "NBK", date: "2016-07-04", base: "BYN", quote: "KZT", mid: 170.73 },
        { provider: "CBA", date: "2016-01-08", base: "BYN", quote: "AMD", mid: 0.026 },
        { provider: "CBA", date: "2016-01-08", base: "BYR", quote: "AMD", mid: 0.026 },
        { provider: "CBA", date: "2016-06-30", base: "BYR", quote: "AMD", mid: 0.024 },
        { provider: "CBA", date: "2016-07-01", base: "BYN", quote: "AMD", mid: 237.78 },
        { provider: "CBA", date: "2016-10-25", base: "BYR", quote: "AMD", mid: 24.991 },
        { provider: "BIS", date: "2010-05-31", base: "USD", quote: "BYN", mid: 0.2998 },
      ]
      DB[:rates].multi_insert(rows)
      CurrencySummary.refresh(DB, rows.flat_map { |row| row.values_at(:base, :quote) })
      [:weekly_rates, :monthly_rates].zip([Bucket.week, Bucket.month]).each do |table, bucket|
        DB[table].insert(
          [:bucket_date, :provider, :base, :quote, :rate],
          DB[:rates].select(bucket, :provider, :base, :quote, Sequel.function(:avg, :rate))
            .group(bucket, :provider, :base, :quote),
        )
      end
      DB[:blended_rates].insert(date: "2010-06-01", quote: "BYN", rate: 2933.8)
      DB[:rate_spikes].insert(provider: "NBK", date: "2016-07-01", base: "BYN", quote: "KZT")
      blends = [BlendedRate, BlendedWeeklyRate, BlendedMonthlyRate]
      snapshot = -> { blends.to_h { |model| [model, model.dataset.order(*model.primary_key).all] } }
      before = snapshot.call

      require "rake"
      load "lib/tasks/db.rake"
      Rake::Task["db:setup"].invoke
      abort "migration incomplete" unless DB[:schema_info].get(:version) >= 47

      stored = DB[:rates].order(:provider, :date, :base).select_map([:provider, :date, :base, :quote, :mid])
        .map { |provider, date, base, quote, mid| [provider, date.to_s, base, quote, mid] }
      expected = [
        ["BIS", "2010-05-31", "USD", "BYN", 0.2998],
        ["CBA", "2016-01-08", "BYR", "AMD", 0.026],
        ["CBA", "2016-06-30", "BYR", "AMD", 0.024],
        ["CBA", "2016-07-01", "BYN", "AMD", 237.78],
        ["CBA", "2016-10-25", "BYN", "AMD", 249.91],
        ["NBK", "2004-09-14", "BYR", "KZT", 0.06],
        ["NBK", "2016-07-01", "BYR", "KZT", 0.0168],
        ["NBK", "2016-07-04", "BYN", "KZT", 170.73],
      ]
      abort "wrong repaired rows: #{stored.inspect}" unless stored == expected

      weekly = ->(provider, base) { DB[:weekly_rates].where(provider:, base:).select_map(:rate) }
      abort "rollup kept old BYN" unless weekly["NBK", "BYN"] == [170.73]
      abort "rollup missed BYR" unless weekly["NBK", "BYR"].sort == [0.0168, 0.06]
      abort "rollup kept stray BYR" unless weekly["CBA", "BYR"].sort == [0.024, 0.026]

      coverage = ->(provider, code) do
        DB[:currency_coverages].where(provider_key: provider, iso_code: code).select_map([:start_date, :end_date])
          .map { |dates| dates.map(&:to_s) }
      end
      abort "NBK BYN coverage" unless coverage["NBK", "BYN"] == [["2016-07-04", "2016-07-04"]]
      abort "NBK BYR coverage" unless coverage["NBK", "BYR"] == [["2004-09-14", "2004-09-14"]]
      abort "CBA BYN coverage" unless coverage["CBA", "BYN"] == [["2016-07-01", "2016-10-25"]]
      abort "CBA BYR coverage" unless coverage["CBA", "BYR"] == [["2016-01-08", "2016-06-30"]]
      abort "BIS BYN coverage" unless coverage["BIS", "BYN"] == [["2010-05-31", "2010-05-31"]]
      start = DB[:currencies].where(iso_code: "BYN").get(:start_date).to_s
      abort "BYN starts on #{start}" unless start == "2016-07-01"

      abort "kept a stale spike flag" unless DB[:rate_spikes].where(provider: "NBK").empty?
      abort "touched the blends" unless snapshot.call == before
      DB.disconnect
    RUBY
  end

  it "rolls back on conflicting duplicates" do
    run_migration_script(<<~RUBY)
      DB[:rates].multi_insert([
        { provider: "NBK", date: "2016-07-01", base: "BYN", quote: "KZT", mid: 0.0168 },
        { provider: "CBA", date: "2016-01-08", base: "BYN", quote: "AMD", mid: 0.026 },
        { provider: "CBA", date: "2016-01-08", base: "BYR", quote: "AMD", mid: 0.025 },
      ])
      DB[:blended_rates].insert(date: "2016-01-08", quote: "BYN", rate: 18_600.0)
      before = DB[:rates].order(:provider, :base).all
      begin
        Sequel::Migrator.run(DB, "db/migrate")
        abort "accepted conflicting components"
      rescue RuntimeError => error
        raise unless error.message.include?("conflicting BYN/BYR components")
      end
      abort "partial repair committed" unless DB[:rates].order(:provider, :base).all == before
      abort "migration marked complete" unless DB[:schema_info].get(:version) == 46
      abort "touched the blends" unless DB[:blended_rates].count == 1
      DB.disconnect
    RUBY
  end

  it "leaves databases without affected rows alone" do
    run_migration_script(<<~RUBY)
      DB[:rates].multi_insert([
        { provider: "NBK", date: "2016-07-04", base: "BYN", quote: "KZT", mid: 170.73 },
        { provider: "CBA", date: "2016-06-30", base: "BYR", quote: "AMD", mid: 0.024 },
        { provider: "BIS", date: "2010-05-31", base: "USD", quote: "BYN", mid: 0.2998 },
      ])
      DB[:blended_rates].insert(date: "2016-07-04", quote: "BYN", rate: 2.0)
      before = DB[:rates].order(:provider).all
      Sequel::Migrator.run(DB, "db/migrate")
      abort "migration incomplete" unless DB[:schema_info].get(:version) >= 47
      abort "changed unaffected rows" unless DB[:rates].order(:provider).all == before
      abort "touched the blends" unless DB[:blended_rates].count == 1
      DB.disconnect
    RUBY
  end
end
