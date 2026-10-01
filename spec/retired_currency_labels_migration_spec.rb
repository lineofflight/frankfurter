# frozen_string_literal: true

require_relative "helper"
require "open3"
require "tmpdir"

describe "Retired currency labels migration" do
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
      initialize = 'require "db"; Sequel.extension(:migration); Sequel::Migrator.run(DB, "db/migrate", target: 40)'
      output, status = Open3.capture2e(env, RbConfig.ruby, "-Ilib", "-r./boot", "-e", initialize)

      _(status.success?).must_equal(true, output)
      output, status = Open3.capture2e(env, RbConfig.ruby, "-Ilib", "-r./boot", "-e", setup + script)

      _(status.success?).must_equal(true, output)
    end
  end

  it "relabels, rescales and drops stored rows and retires legacy series" do
    run_migration_script(<<~'RUBY')
      rows = [
        { provider: "CBU", date: "2008-09-09", base: "SDR", quote: "UZS", mid: 2048.52 },
        { provider: "CBU", date: "2008-09-09", base: "USD", quote: "UZS", mid: 1326.0 },
        { provider: "CBU", date: "2008-09-16", base: "SDR", quote: "UZS", mid: 2044.11 },
        { provider: "CBU", date: "2008-09-16", base: "XDR", quote: "UZS", mid: 2044.11 },
        { provider: "CBU", date: "2008-09-16", base: "USD", quote: "UZS", mid: 1326.38 },
        { provider: "BOTA", date: "1999-07-01", base: "MXM", quote: "TZS", mid: 0.0602 },
        { provider: "BOTA", date: "1999-07-01", base: "USD", quote: "TZS", mid: 740.0 },
        { provider: "NBP", date: "2002-02-26", base: "BYB", quote: "PLN", mid: 0.002494 },
        { provider: "NBP", date: "2002-02-26", base: "USD", quote: "PLN", mid: 4.189 },
        { provider: "NBP", date: "2002-12-24", base: "AFA", quote: "PLN", mid: 0.000816 },
        { provider: "NBP", date: "2002-12-24", base: "USD", quote: "PLN", mid: 3.8388 },
        { provider: "NBP", date: "2003-01-07", base: "AFA", quote: "PLN", mid: 0.089056 },
        { provider: "NBP", date: "2003-01-07", base: "USD", quote: "PLN", mid: 3.8582 },
        { provider: "NBP", date: "2003-10-28", base: "AON", quote: "PLN", mid: 0.0503 },
        { provider: "NBP", date: "2003-10-28", base: "USD", quote: "PLN", mid: 3.9745 },
        { provider: "BOI", date: "2000-01-03", base: "BEL", quote: "ILS", mid: 1.0313 },
        { provider: "BOI", date: "2000-01-03", base: "ATS", quote: "ILS", mid: 3.0234 },
        { provider: "BOI", date: "2000-01-03", base: "ESP", quote: "ILS", mid: 2.5004 },
        { provider: "BOI", date: "2000-01-03", base: "ITL", quote: "ILS", mid: 2.1486 },
        { provider: "BOI", date: "2000-01-03", base: "CBK_L", quote: "ILS", mid: 4.387 },
        { provider: "BOI", date: "2000-01-03", base: "USD", quote: "ILS", mid: 4.124 },
        { provider: "BDI", date: "1999-07-02", base: "EUR", quote: "BGL", mid: 1955.83 },
        { provider: "BDI", date: "1999-07-02", base: "EUR", quote: "USD", mid: 1.0315 },
        { provider: "BDI", date: "2001-06-01", base: "EUR", quote: "BGL", mid: 1947.0 },
        { provider: "BDI", date: "2001-06-01", base: "EUR", quote: "BGN", mid: 1.947 },
        { provider: "BDI", date: "2001-06-01", base: "EUR", quote: "USD", mid: 0.85 },
        { provider: "ECB", date: "2026-01-20", base: "EUR", quote: "USD", mid: 1.17 },
        { provider: "ECB", date: "2026-01-20", base: "EUR", quote: "GBP", mid: 0.87 },
      ]
      DB[:rates].multi_insert(rows)
      CurrencySummary.refresh(DB, rows.flat_map { |row| row.values_at(:base, :quote) })
      # Before its defunct entry, BGL's coverage ran to BDI's last quote.
      DB[:currency_coverages].where(provider_key: "BDI", iso_code: "BGL").update(end_date: "2001-06-01")
      DB[:currencies].where(iso_code: "BGL").update(end_date: "2001-06-01")
      [:weekly_rates, :monthly_rates].zip([Bucket.week, Bucket.month]).each do |table, bucket|
        DB[table].insert(
          [:bucket_date, :provider, :base, :quote, :rate],
          DB[:rates].select(bucket, :provider, :base, :quote, Sequel.function(:avg, :rate))
            .group(bucket, :provider, :base, :quote),
        )
      end
      [BlendedRate, BlendedWeeklyRate, BlendedMonthlyRate].each(&:rebuild)
      unaffected_bucket = DB.get(Bucket.week("2026-01-20"))
      unaffected = DB[:blended_weekly_rates].where(bucket_date: unaffected_bucket).all
      abort "missing unaffected fixture" if unaffected.empty?
      expired_bucket = DB.get(Bucket.week("2001-06-01"))
      abort "missing expired fixture" if DB[:blended_weekly_rates].where(bucket_date: expired_bucket).empty?

      require "rake"
      load "lib/tasks/db.rake"
      Rake::Task["db:setup"].invoke
      abort "migration incomplete" unless DB[:schema_info].get(:version) >= 41

      stored = DB[:rates].exclude(provider: ["BDI", "ECB"]).order(:provider, :date, :base)
        .select_map([:provider, :date, :base, :mid]).map { |provider, date, base, mid| [provider, date.to_s, base, mid] }
      expected = [
        ["BOI", "2000-01-03", "ATS", 0.30234],
        ["BOI", "2000-01-03", "BEF", 0.10313],
        ["BOI", "2000-01-03", "ESP", 0.025004],
        ["BOI", "2000-01-03", "ITL", 0.0021486],
        ["BOI", "2000-01-03", "USD", 4.124],
        ["BOTA", "1999-07-01", "MZM", 0.0602],
        ["BOTA", "1999-07-01", "USD", 740.0],
        ["CBU", "2008-09-09", "USD", 1326.0],
        ["CBU", "2008-09-09", "XDR", 2048.52],
        ["CBU", "2008-09-16", "USD", 1326.38],
        ["CBU", "2008-09-16", "XDR", 2044.11],
        ["NBP", "2002-02-26", "BYR", 0.002494],
        ["NBP", "2002-02-26", "USD", 4.189],
        ["NBP", "2002-12-24", "AFA", 0.000816],
        ["NBP", "2002-12-24", "USD", 3.8388],
        ["NBP", "2003-01-07", "AFN", 0.089056],
        ["NBP", "2003-01-07", "USD", 3.8582],
        ["NBP", "2003-10-28", "AOA", 0.0503],
        ["NBP", "2003-10-28", "USD", 3.9745],
      ]
      abort "wrong repaired rows: #{stored.inspect}" unless stored == expected
      abort "touched BDI rows" unless DB[:rates].where(provider: "BDI").count == 5

      ["CBU", "BOTA", "NBP", "BOI"].each do |key|
        abort "#{key} still reports #{Provider[key].unknown_currencies}" unless Provider[key].unknown_currencies.empty?
      end
      ["SDR", "MXM", "AON", "BEL", "CBK_L"].each do |code|
        abort "#{code} rollup retained" unless DB[:weekly_rates].where(base: code).empty?
      end
      abort "stale ATS rollup" unless DB[:monthly_rates].where(provider: "BOI", base: "ATS").get(:rate) == 0.30234
      coverage = DB[:currency_coverages].where(provider_key: "BDI", iso_code: "BGL").first
      abort "BGL coverage past retirement" unless coverage[:end_date].to_s == "1999-07-02"
      abort "BGL catalogue past retirement" unless DB[:currencies].where(iso_code: "BGL").get(:end_date).to_s == "1999-07-02"
      abort "AFN coverage missing" unless DB[:currency_coverages].where(provider_key: "NBP", iso_code: "AFN").count == 1

      abort "daily blend stayed ready" if BlendedRate.ready?
      abort "partial daily invalidation" unless DB[:blended_rates].empty?
      abort "changed unrelated bucket" unless DB[:blended_weekly_rates].where(bucket_date: unaffected_bucket).all == unaffected
      abort "kept expired BGL bucket" unless DB[:blended_weekly_rates].where(bucket_date: expired_bucket).empty?
      [BlendedWeeklyRate, BlendedMonthlyRate].each do |model|
        abort "#{model} stayed ready" if model.ready?
      end

      # Exercise the same recovery lifecycle as bin/schedule, then compare it with a clean full rebuild.
      BlendedRate.rebuild
      [BlendedWeeklyRate, BlendedMonthlyRate].each(&:populate)
      abort "expired BGL blended" unless DB[:blended_rates].where(quote: "BGL").where { date >= "1999-07-05" }.empty?
      abort "relabelled XDR missing" if DB[:blended_rates].where(quote: "XDR", date: "2008-09-09").empty?
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
        { provider: "CBU", date: "2008-09-16", base: "SDR", quote: "UZS", mid: 2044.11 },
        { provider: "CBU", date: "2008-09-16", base: "XDR", quote: "UZS", mid: 2044.12 },
      ])
      DB[:blended_rates].insert(date: "2008-09-16", quote: "UZS", rate: 1326.38)
      before = DB[:rates].order(:base).all
      begin
        Sequel::Migrator.run(DB, "db/migrate")
        abort "accepted conflicting components"
      rescue RuntimeError => error
        raise unless error.message.include?("conflicting SDR/XDR components")
      end
      abort "partial repair committed" unless DB[:rates].order(:base).all == before
      abort "migration marked complete" unless DB[:schema_info].get(:version) == 40
      abort "invalidated blends after failure" unless DB[:blended_rates].count == 1
      DB.disconnect
    RUBY
  end

  it "leaves databases without affected history alone" do
    run_migration_script(<<~RUBY)
      DB[:rates].insert(provider: "ECB", date: "2026-01-20", base: "EUR", quote: "USD", mid: 1.17)
      DB[:blended_rates].insert(date: "2026-01-20", quote: "EUR", rate: 0.85)
      Sequel::Migrator.run(DB, "db/migrate")
      abort "unnecessary invalidation" unless DB[:blended_rates].count == 1
      DB.disconnect
    RUBY
  end
end
