# frozen_string_literal: true

require_relative "helper"
require "open3"
require "tmpdir"

describe "Successor values migration" do
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
      initialize = 'require "db"; Sequel.extension(:migration); Sequel::Migrator.run(DB, "db/migrate", target: 42)'
      output, status = Open3.capture2e(env, RbConfig.ruby, "-Ilib", "-r./boot", "-e", initialize)

      _(status.success?).must_equal(true, output)
      output, status = Open3.capture2e(env, RbConfig.ruby, "-Ilib", "-r./boot", "-e", setup + script)

      _(status.success?).must_equal(true, output)
    end
  end

  it "relabels, rescales and drops stored rows and leaves the blends to a rebuild" do
    run_migration_script(<<~'RUBY')
      rows = [
        { provider: "CBG", date: "2021-08-30", base: "SLL", quote: "GMD", mid: 0.01 },
        { provider: "CBG", date: "2022-07-12", base: "SLL", quote: "GMD", mid: 4.11 },
        { provider: "CBU", date: "2004-12-28", base: "TRL", quote: "UZS", mid: 0.00078 },
        { provider: "CBU", date: "2005-01-04", base: "TRL", quote: "UZS", mid: 787.09 },
        { provider: "CBU", date: "2009-02-17", base: "TRL", quote: "UZS", mid: 849.84 },
        { provider: "CBU", date: "2009-02-17", base: "TRY", quote: "UZS", mid: 849.84 },
        { provider: "LB", date: "1999-12-31", base: "BYR", quote: "LTL", mid: 0.000004444 },
        { provider: "LB", date: "2000-01-03", base: "BYR", quote: "LTL", mid: 0.0044444 },
        { provider: "NBU", date: "1999-01-04", base: "RUR", quote: "UAH", mid: 0.16596 },
        { provider: "NBU", date: "2004-03-31", base: "RUR", quote: "UAH", mid: 0.18709 },
        { provider: "NBU", date: "2004-03-31", base: "RUB", quote: "UAH", mid: 0.18709 },
        { provider: "NBU", date: "1999-08-02", base: "BGL", quote: "UAH", mid: 2.2594804 },
        { provider: "NBU", date: "2000-01-03", base: "BGL", quote: "UAH", mid: 0.2712867 },
        { provider: "NBU", date: "2005-01-05", base: "TRL", quote: "UAH", mid: 0.00000376 },
        { provider: "NBU", date: "2005-06-27", base: "TRL", quote: "UAH", mid: 0.03729741 },
        { provider: "NBU", date: "2005-06-27", base: "USD", quote: "UAH", mid: 5.055 },
        { provider: "BNA", date: "2014-04-22", base: "MZM", quote: "AOA", mid: 3.1 },
        { provider: "BNA", date: "2023-02-17", base: "STD", quote: "AOA", mid: 0.02398 },
        { provider: "BNA", date: "2023-10-18", base: "VEF", quote: "AOA", mid: 23.74128 },
        { provider: "BNA", date: "2026-02-05", base: "VEF", quote: "AOA", mid: 2.48819 },
        { provider: "BNA", date: "2026-02-05", base: "VES", quote: "AOA", mid: 2.487 },
        { provider: "BDI", date: "2008-07-31", base: "EUR", quote: "ZWD", mid: 108471581765.0 },
        { provider: "BDI", date: "2008-08-01", base: "EUR", quote: "ZWD", mid: 11.805092 },
        { provider: "BDI", date: "2009-02-03", base: "EUR", quote: "ZWD", mid: 28.2678 },
        { provider: "NBP", date: "2009-02-04", base: "ZWR", quote: "PLN", mid: 1.0e-08 },
        { provider: "NBP", date: "2009-02-25", base: "ZWR", quote: "PLN", mid: 0.043749 },
        { provider: "BAM", date: "2018-01-02", base: "MRO", quote: "MAD", mid: 0.02624 },
        { provider: "BAM", date: "2018-04-16", base: "MRO", quote: "MAD", mid: 0.25864 },
        { provider: "BAM", date: "2018-04-17", base: "MRO", quote: "MAD", mid: 25.857 },
        { provider: "BAM", date: "2018-04-17", base: "USD", quote: "MAD", mid: 9.1664 },
        { provider: "BOTA", date: "2025-06-21", base: "ZMK", quote: "TZS", mid: 111.6619 },
        { provider: "NBKR", date: "2016-06-25", base: "BYR", quote: "KGS", mid: 0.003402 },
        { provider: "NBKR", date: "2026-09-26", base: "BYR", quote: "KGS", mid: 0.003402 },
        { provider: "ECB", date: "2026-01-20", base: "EUR", quote: "USD", mid: 1.17 },
        { provider: "ECB", date: "2026-01-20", base: "EUR", quote: "GBP", mid: 0.87 },
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
      blends = [BlendedRate, BlendedWeeklyRate, BlendedMonthlyRate]
      blends.each(&:rebuild)
      abort "fixture blend missing TRL" if DB[:blended_rates].where(quote: "TRL", date: "2005-06-27").empty?
      snapshot = -> { blends.to_h { |model| [model, model.dataset.order(*model.primary_key).all] } }
      before = snapshot.call

      require "rake"
      load "lib/tasks/db.rake"
      Rake::Task["db:setup"].invoke
      abort "migration incomplete" unless DB[:schema_info].get(:version) >= 43

      stored = DB[:rates].exclude(provider: "ECB").exclude(base: "USD").order(:provider, :date, :base, :quote)
        .select_map([:provider, :date, :base, :quote, :mid])
        .map { |provider, date, base, quote, mid| [provider, date.to_s, base, quote, mid] }
      expected = [
        ["BAM", "2018-01-02", "MRO", "MAD", 0.02624],
        ["BAM", "2018-04-16", "MRU", "MAD", 0.25864],
        ["BAM", "2018-04-17", "MRU", "MAD", 0.25857],
        ["BDI", "2008-07-31", "EUR", "ZWD", 108471581765.0],
        ["BDI", "2008-08-01", "EUR", "ZWR", 11.805092],
        ["BDI", "2009-02-03", "EUR", "ZWL", 28.2678],
        ["BNA", "2014-04-22", "MZN", "AOA", 3.1],
        ["BNA", "2023-02-17", "STD", "AOA", 0.02398],
        ["BNA", "2023-10-18", "VES", "AOA", 23.74128],
        ["BNA", "2026-02-05", "VES", "AOA", 2.487],
        ["BOTA", "2025-06-21", "ZMW", "TZS", 111.6619],
        ["CBG", "2021-08-30", "SLL", "GMD", 0.01],
        ["CBG", "2022-07-12", "SLE", "GMD", 4.11],
        ["CBU", "2004-12-28", "TRL", "UZS", 0.00078],
        ["CBU", "2005-01-04", "TRY", "UZS", 787.09],
        ["CBU", "2009-02-17", "TRY", "UZS", 849.84],
        ["LB", "1999-12-31", "BYB", "LTL", 0.000004444],
        ["LB", "2000-01-03", "BYR", "LTL", 0.0044444],
        ["NBKR", "2016-06-25", "BYR", "KGS", 0.003402],
        ["NBP", "2009-02-04", "ZWR", "PLN", 1.0e-08],
        ["NBP", "2009-02-25", "ZWL", "PLN", 0.043749],
        ["NBU", "1999-01-04", "RUB", "UAH", 0.16596],
        ["NBU", "1999-08-02", "BGN", "UAH", 2.2594804],
        ["NBU", "2000-01-03", "BGN", "UAH", 2.712867],
        ["NBU", "2004-03-31", "RUB", "UAH", 0.18709],
        ["NBU", "2005-01-05", "TRL", "UAH", 0.00000376],
        ["NBU", "2005-06-27", "TRY", "UAH", 3.729741],
      ]
      abort "wrong repaired rows: #{stored.inspect}" unless stored == expected

      week = DB.get(Bucket.week("2005-06-27"))
      nbu = DB[:weekly_rates].where(provider: "NBU", bucket_date: week).exclude(base: "USD").select_map([:base, :rate])
      abort "stale NBU rollup: #{nbu.inspect}" unless nbu == [["TRY", 3.729741]]
      abort "BAM rollup kept MRO" unless DB[:monthly_rates].where(provider: "BAM", base: "MRO")
        .select_map(:bucket_date).map(&:to_s) == [DB.get(Bucket.month("2018-01-02")).to_s]
      abort "NBKR rollup kept 2026 BYR" unless DB[:weekly_rates].where(provider: "NBKR").count == 1

      coverage = ->(provider, code) do
        DB[:currency_coverages].where(provider_key: provider, iso_code: code).select_map([:start_date, :end_date])
          .map { |dates| dates.map(&:to_s) }
      end
      abort "TRL coverage past 2005" unless coverage["NBU", "TRL"] == [["2005-01-05", "2005-01-05"]]
      abort "RUR coverage kept" unless coverage["NBU", "RUR"].empty?
      abort "SLE coverage missing" unless coverage["CBG", "SLE"] == [["2022-07-12", "2022-07-12"]]
      abort "BYB coverage missing" unless coverage["LB", "BYB"] == [["1999-12-31", "1999-12-31"]]
      abort "MRU catalogue missing" unless DB[:currencies].where(iso_code: "MRU").get(:start_date).to_s == "2018-04-16"

      abort "touched the blends" unless snapshot.call == before

      blends.each(&:rebuild)
      abort "TRL still blended in 2005" unless DB[:blended_rates].where(quote: "TRL", date: "2005-06-27").empty?
      try = DB[:blended_rates].where(quote: "TRY", date: "2005-06-27").get(:rate)
      abort "TRY blend #{try.inspect}" unless try && (try - (5.055 / 3.729741)).abs < 1e-9
      mru = DB[:blended_rates].where(quote: "MRU", date: "2018-04-17").get(:rate)
      abort "MRU blend #{mru.inspect}" unless mru && (mru - (9.1664 / 0.25857)).abs < 1e-9
      DB.disconnect
    RUBY
  end

  it "rolls back on conflicting duplicates" do
    run_migration_script(<<~RUBY)
      DB[:rates].multi_insert([
        { provider: "CBU", date: "2009-02-17", base: "TRL", quote: "UZS", mid: 849.84 },
        { provider: "CBU", date: "2009-02-17", base: "TRY", quote: "UZS", mid: 849.85 },
      ])
      DB[:blended_rates].insert(date: "2009-02-17", quote: "UZS", rate: 1390.0)
      before = DB[:rates].order(:base).all
      begin
        Sequel::Migrator.run(DB, "db/migrate")
        abort "accepted conflicting components"
      rescue RuntimeError => error
        raise unless error.message.include?("conflicting TRL/TRY components")
      end
      abort "partial repair committed" unless DB[:rates].order(:base).all == before
      abort "migration marked complete" unless DB[:schema_info].get(:version) == 42
      abort "touched the blends" unless DB[:blended_rates].count == 1
      DB.disconnect
    RUBY
  end

  it "leaves databases without affected history alone" do
    run_migration_script(<<~RUBY)
      DB[:rates].multi_insert([
        { provider: "CBU", date: "2004-12-28", base: "TRL", quote: "UZS", mid: 0.00078 },
        { provider: "NBU", date: "2014-04-04", base: "TRY", quote: "UAH", mid: 5.416837 },
      ])
      DB[:blended_rates].insert(date: "2014-04-04", quote: "TRY", rate: 2.14)
      before = DB[:rates].order(:provider).all
      Sequel::Migrator.run(DB, "db/migrate")
      abort "migration incomplete" unless DB[:schema_info].get(:version) >= 43
      abort "changed unaffected rows" unless DB[:rates].order(:provider).all == before
      abort "touched the blends" unless DB[:blended_rates].count == 1
      DB.disconnect
    RUBY
  end
end
