# frozen_string_literal: true

require_relative "helper"
require "open3"
require "tmpdir"

describe "NBRM ECU migration" do
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
      initialize = 'require "db"; Sequel.extension(:migration); Sequel::Migrator.run(DB, "db/migrate", target: 43)'
      output, status = Open3.capture2e(env, RbConfig.ruby, "-Ilib", "-r./boot", "-e", initialize)

      _(status.success?).must_equal(true, output)
      output, status = Open3.capture2e(env, RbConfig.ruby, "-Ilib", "-r./boot", "-e", setup + script)

      _(status.success?).must_equal(true, output)
    end
  end

  it "relabels the ECU, collapses its euro duplicates and leaves the blends to a rebuild" do
    run_migration_script(<<~'RUBY')
      rows = [
        { provider: "NBRM", date: "1996-06-03", base: "XBA", quote: "MKD", mid: 50.2099 },
        { provider: "NBRM", date: "1996-06-03", base: "USD", quote: "MKD", mid: 40.781 },
        { provider: "NBRM", date: "1998-12-31", base: "XBA", quote: "MKD", mid: 60.9144 },
        { provider: "NBRM", date: "1999-01-04", base: "XBA", quote: "MKD", mid: 60.5994 },
        { provider: "NBRM", date: "1999-01-04", base: "EUR", quote: "MKD", mid: 60.5994 },
        { provider: "NBRM", date: "1999-05-04", base: "XBA", quote: "MKD", mid: 60.6199 },
        { provider: "NBRM", date: "1999-05-04", base: "EUR", quote: "MKD", mid: 60.6199 },
        { provider: "CNB", date: "1996-06-03", base: "XEU", quote: "CZK", mid: 34.316 },
        { provider: "CNB", date: "1996-06-03", base: "USD", quote: "CZK", mid: 27.86 },
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
      abort "fixture blend missing XBA" if DB[:blended_rates].where(quote: "XBA", date: "1996-06-03").empty?
      abort "fixture catalogue missing XBA" if DB[:currencies].where(iso_code: "XBA").empty?
      snapshot = -> { blends.to_h { |model| [model, model.dataset.order(*model.primary_key).all] } }
      before = snapshot.call

      require "rake"
      load "lib/tasks/db.rake"
      Rake::Task["db:setup"].invoke
      abort "migration incomplete" unless DB[:schema_info].get(:version) >= 44

      stored = DB[:rates].exclude(base: "USD").order(:provider, :date, :base, :quote)
        .select_map([:provider, :date, :base, :quote, :mid])
        .map { |provider, date, base, quote, mid| [provider, date.to_s, base, quote, mid] }
      expected = [
        ["CNB", "1996-06-03", "XEU", "CZK", 34.316],
        ["NBRM", "1996-06-03", "XEU", "MKD", 50.2099],
        ["NBRM", "1998-12-31", "XEU", "MKD", 60.9144],
        ["NBRM", "1999-01-04", "EUR", "MKD", 60.5994],
        ["NBRM", "1999-05-04", "EUR", "MKD", 60.6199],
      ]
      abort "wrong repaired rows: #{stored.inspect}" unless stored == expected

      abort "rollup kept XBA" unless DB[:weekly_rates].where(base: "XBA").empty? &&
        DB[:monthly_rates].where(base: "XBA").empty?
      week = DB.get(Bucket.week("1999-05-04"))
      eur = DB[:weekly_rates].where(provider: "NBRM", bucket_date: week, base: "EUR").select_map(:rate)
      abort "stale EUR rollup: #{eur.inspect}" unless eur == [60.6199]

      coverage = ->(code) do
        DB[:currency_coverages].where(provider_key: "NBRM", iso_code: code).select_map([:start_date, :end_date])
          .map { |dates| dates.map(&:to_s) }
      end
      abort "XBA coverage kept" unless coverage["XBA"].empty?
      abort "XEU coverage #{coverage["XEU"].inspect}" unless coverage["XEU"] == [["1996-06-03", "1998-12-31"]]
      abort "EUR coverage #{coverage["EUR"].inspect}" unless coverage["EUR"] == [["1999-01-04", "1999-05-04"]]
      abort "XBA still catalogued" unless DB[:currencies].where(iso_code: "XBA").empty?

      abort "touched the blends" unless snapshot.call == before

      blends.each(&:rebuild)
      abort "XBA still blended" unless DB[:blended_rates].where(quote: "XBA").empty?
      abort "XEU blend missing" if DB[:blended_rates].where(quote: "XEU", date: "1996-06-03").empty?
      DB.disconnect
    RUBY
  end

  it "rolls back on conflicting duplicates" do
    run_migration_script(<<~RUBY)
      DB[:rates].multi_insert([
        { provider: "NBRM", date: "1999-01-04", base: "XBA", quote: "MKD", mid: 60.5994 },
        { provider: "NBRM", date: "1999-01-04", base: "EUR", quote: "MKD", mid: 60.5995 },
      ])
      DB[:blended_rates].insert(date: "1999-01-04", quote: "MKD", rate: 36.0)
      before = DB[:rates].order(:base).all
      begin
        Sequel::Migrator.run(DB, "db/migrate")
        abort "accepted conflicting components"
      rescue RuntimeError => error
        raise unless error.message.include?("conflicting XBA/EUR components")
      end
      abort "partial repair committed" unless DB[:rates].order(:base).all == before
      abort "migration marked complete" unless DB[:schema_info].get(:version) == 43
      abort "touched the blends" unless DB[:blended_rates].count == 1
      DB.disconnect
    RUBY
  end

  it "leaves databases without NBRM XBA history alone" do
    run_migration_script(<<~RUBY)
      DB[:rates].multi_insert([
        { provider: "NBRM", date: "1999-01-04", base: "EUR", quote: "MKD", mid: 60.5994 },
        { provider: "CBBH", date: "1998-01-06", base: "XEU", quote: "BAM", mid: 1.97509972 },
      ])
      DB[:blended_rates].insert(date: "1999-01-04", quote: "MKD", rate: 36.0)
      before = DB[:rates].order(:provider).all
      Sequel::Migrator.run(DB, "db/migrate")
      abort "migration incomplete" unless DB[:schema_info].get(:version) >= 44
      abort "changed unaffected rows" unless DB[:rates].order(:provider).all == before
      abort "touched the blends" unless DB[:blended_rates].count == 1
      DB.disconnect
    RUBY
  end
end
