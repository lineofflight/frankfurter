# frozen_string_literal: true

require_relative "helper"
require "open3"
require "tmpdir"

describe "LB early history migration" do
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
      initialize = 'require "db"; Sequel.extension(:migration); Sequel::Migrator.run(DB, "db/migrate", target: 47)'
      output, status = Open3.capture2e(env, RbConfig.ruby, "-Ilib", "-r./boot", "-e", initialize)

      _(status.success?).must_equal(true, output)
      output, status = Open3.capture2e(env, RbConfig.ruby, "-Ilib", "-r./boot", "-e", setup + script)

      _(status.success?).must_equal(true, output)
    end
  end

  it "drops ruble copies, rescales the old Belarusian ruble and leaves the blends to a rebuild" do
    run_migration_script(<<~'RUBY')
      rows = [
        { provider: "LB", date: "1995-07-10", base: "RUR", quote: "LTL", mid: 0.000873 },
        { provider: "LB", date: "1995-07-10", base: "GER", quote: "LTL", mid: 0.000873 },
        { provider: "LB", date: "1995-07-10", base: "TJR", quote: "LTL", mid: 0.000873 },
        { provider: "LB", date: "1995-07-11", base: "TJR", quote: "LTL", mid: 0.074074 },
        { provider: "LB", date: "1995-11-29", base: "TMM", quote: "LTL", mid: 0.000874 },
        { provider: "LB", date: "1995-11-30", base: "TMM", quote: "LTL", mid: 0.002759 },
        { provider: "LB", date: "1994-08-19", base: "BYB", quote: "LTL", mid: 0.00014 },
        { provider: "LB", date: "1994-08-22", base: "BYB", quote: "LTL", mid: 0.001421 },
        { provider: "LB", date: "1996-05-07", base: "YUN", quote: "LTL", mid: 0.7935 },
        { provider: "INFOREURO", date: "1995-02-01", base: "XEU", quote: "BYB", mid: 9000.0 },
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
      DB[:blended_rates].insert(date: "1995-07-10", quote: "TMM", rate: 5000.0)
      DB[:rate_spikes].insert(provider: "LB", date: "1994-08-19", base: "BYB", quote: "LTL")
      blends = [BlendedRate, BlendedWeeklyRate, BlendedMonthlyRate]
      snapshot = -> { blends.to_h { |model| [model, model.dataset.order(*model.primary_key).all] } }
      before = snapshot.call

      require "rake"
      load "lib/tasks/db.rake"
      Rake::Task["db:setup"].invoke
      abort "migration incomplete" unless DB[:schema_info].get(:version) >= 48

      stored = DB[:rates].order(:provider, :date, :base).select_map([:provider, :date, :base, :quote, :mid])
        .map { |provider, date, base, quote, mid| [provider, date.to_s, base, quote, mid] }
      expected = [
        ["INFOREURO", "1995-02-01", "XEU", "BYB", 9000.0],
        ["LB", "1994-08-19", "BYB", "LTL", 0.0014],
        ["LB", "1994-08-22", "BYB", "LTL", 0.001421],
        ["LB", "1995-07-10", "RUR", "LTL", 0.000873],
        ["LB", "1995-07-11", "TJR", "LTL", 0.074074],
        ["LB", "1995-11-30", "TMM", "LTL", 0.002759],
        ["LB", "1996-05-07", "YUM", "LTL", 0.7935],
      ]
      abort "wrong repaired rows: #{stored.inspect}" unless stored == expected

      weekly = ->(base) { DB[:weekly_rates].where(provider: "LB", base:).select_map(:rate) }
      abort "rollup kept a copy" unless weekly["TMM"] == [0.002759] && weekly["GER"].empty?
      abort "rollup kept the old ruble" unless weekly["BYB"].sort == [0.0014, 0.001421]
      abort "rollup kept YUN" unless weekly["YUN"].empty? && weekly["YUM"] == [0.7935]

      coverage = ->(code) do
        DB[:currency_coverages].where(provider_key: "LB", iso_code: code).select_map([:start_date, :end_date])
          .map { |dates| dates.map(&:to_s) }
      end
      abort "TMM coverage" unless coverage["TMM"] == [["1995-11-30", "1995-11-30"]]
      abort "TJR coverage" unless coverage["TJR"] == [["1995-07-11", "1995-07-11"]]
      abort "YUM coverage" unless coverage["YUM"] == [["1996-05-07", "1996-05-07"]]
      abort "kept unknown codes" unless DB[:currency_exclusions].where(provider_key: "LB").empty?

      abort "kept a stale spike flag" unless DB[:rate_spikes].where(provider: "LB").empty?
      abort "touched the blends" unless snapshot.call == before
      DB.disconnect
    RUBY
  end

  it "leaves databases without affected rows alone" do
    run_migration_script(<<~RUBY)
      DB[:rates].multi_insert([
        { provider: "LB", date: "1995-11-30", base: "TMM", quote: "LTL", mid: 0.002759 },
        { provider: "LB", date: "1994-08-22", base: "BYB", quote: "LTL", mid: 0.001421 },
        { provider: "INFOREURO", date: "1995-02-01", base: "XEU", quote: "BYB", mid: 9000.0 },
      ])
      DB[:blended_rates].insert(date: "1995-11-30", quote: "TMM", rate: 1450.0)
      before = DB[:rates].order(:provider).all
      Sequel::Migrator.run(DB, "db/migrate")
      abort "migration incomplete" unless DB[:schema_info].get(:version) >= 48
      abort "changed unaffected rows" unless DB[:rates].order(:provider).all == before
      abort "touched the blends" unless DB[:blended_rates].count == 1
      DB.disconnect
    RUBY
  end
end
