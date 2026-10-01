# frozen_string_literal: true

require_relative "helper"
require "open3"
require "tmpdir"

describe "LB and CBA units migration" do
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
      initialize = 'require "db"; Sequel.extension(:migration); Sequel::Migrator.run(DB, "db/migrate", target: 45)'
      output, status = Open3.capture2e(env, RbConfig.ruby, "-Ilib", "-r./boot", "-e", initialize)

      _(status.success?).must_equal(true, output)
      output, status = Open3.capture2e(env, RbConfig.ruby, "-Ilib", "-r./boot", "-e", setup + script)

      _(status.success?).must_equal(true, output)
    end
  end

  it "relabels and rescales stored rows and leaves the blends to a rebuild" do
    run_migration_script(<<~'RUBY')
      rows = [
        { provider: "LB", date: "1995-01-02", base: "PLN", quote: "LTL", mid: 0.0001641 },
        { provider: "LB", date: "1995-01-03", base: "PLN", quote: "LTL", mid: 1.646 },
        { provider: "LB", date: "1998-01-02", base: "RUB", quote: "LTL", mid: 0.0006694 },
        { provider: "LB", date: "1998-01-05", base: "RUB", quote: "LTL", mid: 0.6672 },
        { provider: "LB", date: "1999-07-06", base: "BGN", quote: "LTL", mid: 0.0021467 },
        { provider: "LB", date: "1999-07-07", base: "BGN", quote: "LTL", mid: 2.0952 },
        { provider: "LB", date: "2005-07-01", base: "RON", quote: "LTL", mid: 0.000095538 },
        { provider: "LB", date: "2005-07-04", base: "RON", quote: "LTL", mid: 0.95814 },
        { provider: "LB", date: "2006-07-07", base: "MZN", quote: "LTL", mid: 0.00010536 },
        { provider: "LB", date: "2006-07-10", base: "MZN", quote: "LTL", mid: 0.10531 },
        { provider: "CBA", date: "2000-10-30", base: "TJS", quote: "AMD", mid: 2.671 },
        { provider: "CBA", date: "2000-11-01", base: "TJS", quote: "AMD", mid: 250.74 },
        { provider: "CBA", date: "2004-12-30", base: "KZT", quote: "AMD", mid: 37.37 },
        { provider: "CBA", date: "2005-01-04", base: "KZT", quote: "AMD", mid: 3.739 },
        { provider: "NBU", date: "1997-12-31", base: "RUR", quote: "UAH", mid: 0.000315 },
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
      DB[:blended_rates].insert(date: "1995-01-02", quote: "PLN", rate: 24_495.0)
      DB[:rate_spikes].insert(provider: "LB", date: "1995-01-02", base: "PLN", quote: "LTL")
      blends = [BlendedRate, BlendedWeeklyRate, BlendedMonthlyRate]
      snapshot = -> { blends.to_h { |model| [model, model.dataset.order(*model.primary_key).all] } }
      before = snapshot.call

      require "rake"
      load "lib/tasks/db.rake"
      Rake::Task["db:setup"].invoke
      abort "migration incomplete" unless DB[:schema_info].get(:version) >= 46

      stored = DB[:rates].order(:provider, :date, :base).select_map([:provider, :date, :base, :mid])
        .map { |provider, date, base, mid| [provider, date.to_s, base, mid] }
      expected = [
        ["CBA", "2000-10-30", "TJR", 0.2671],
        ["CBA", "2000-11-01", "TJS", 250.74],
        ["CBA", "2004-12-30", "KZT", 3.737],
        ["CBA", "2005-01-04", "KZT", 3.739],
        ["LB", "1995-01-02", "PLZ", 0.0001641],
        ["LB", "1995-01-03", "PLN", 1.646],
        ["LB", "1998-01-02", "RUR", 0.0006694],
        ["LB", "1998-01-05", "RUB", 0.6672],
        ["LB", "1999-07-06", "BGL", 0.0021467],
        ["LB", "1999-07-07", "BGN", 2.0952],
        ["LB", "2005-07-01", "ROL", 0.000095538],
        ["LB", "2005-07-04", "RON", 0.95814],
        ["LB", "2006-07-07", "MZM", 0.00010536],
        ["LB", "2006-07-10", "MZN", 0.10531],
        ["NBU", "1997-12-31", "RUR", 0.000315],
      ]
      abort "wrong repaired rows: #{stored.inspect}" unless stored == expected

      month = DB.get(Bucket.month("2004-12-30"))
      kzt = DB[:monthly_rates].where(provider: "CBA", bucket_date: month, base: "KZT").select_map(:rate)
      abort "stale KZT rollup: #{kzt.inspect}" unless kzt == [3.737]
      abort "rollup kept PLN" unless DB[:weekly_rates].where(provider: "LB", base: "PLN").select_map(:rate) == [1.646]

      coverage = ->(provider, code) do
        DB[:currency_coverages].where(provider_key: provider, iso_code: code).select_map([:start_date, :end_date])
          .map { |dates| dates.map(&:to_s) }
      end
      abort "PLN coverage" unless coverage["LB", "PLN"] == [["1995-01-03", "1995-01-03"]]
      abort "PLZ coverage" unless coverage["LB", "PLZ"] == [["1995-01-02", "1995-01-02"]]
      abort "TJR coverage" unless coverage["CBA", "TJR"] == [["2000-10-30", "2000-10-30"]]
      abort "NBU RUR coverage" unless coverage["NBU", "RUR"] == [["1997-12-31", "1997-12-31"]]

      abort "kept a stale spike flag" unless DB[:rate_spikes].where(provider: "LB").empty?
      abort "touched the blends" unless snapshot.call == before
      DB.disconnect
    RUBY
  end

  it "rolls back on conflicting duplicates" do
    run_migration_script(<<~RUBY)
      DB[:rates].multi_insert([
        { provider: "LB", date: "1995-01-02", base: "PLN", quote: "LTL", mid: 0.0001641 },
        { provider: "LB", date: "1995-01-02", base: "PLZ", quote: "LTL", mid: 0.0001642 },
      ])
      DB[:blended_rates].insert(date: "1995-01-02", quote: "PLN", rate: 24_495.0)
      before = DB[:rates].order(:base).all
      begin
        Sequel::Migrator.run(DB, "db/migrate")
        abort "accepted conflicting components"
      rescue RuntimeError => error
        raise unless error.message.include?("conflicting PLN/PLZ components")
      end
      abort "partial repair committed" unless DB[:rates].order(:base).all == before
      abort "migration marked complete" unless DB[:schema_info].get(:version) == 45
      abort "touched the blends" unless DB[:blended_rates].count == 1
      DB.disconnect
    RUBY
  end

  it "leaves databases without affected rows alone" do
    run_migration_script(<<~RUBY)
      DB[:rates].multi_insert([
        { provider: "LB", date: "1995-01-03", base: "PLN", quote: "LTL", mid: 1.646 },
        { provider: "CBA", date: "2005-01-04", base: "KZT", quote: "AMD", mid: 3.739 },
        { provider: "NBP", date: "1994-12-30", base: "USD", quote: "PLN", mid: 2.4372 },
      ])
      DB[:blended_rates].insert(date: "1995-01-03", quote: "PLN", rate: 2.43)
      before = DB[:rates].order(:provider).all
      Sequel::Migrator.run(DB, "db/migrate")
      abort "migration incomplete" unless DB[:schema_info].get(:version) >= 46
      abort "changed unaffected rows" unless DB[:rates].order(:provider).all == before
      abort "touched the blends" unless DB[:blended_rates].count == 1
      DB.disconnect
    RUBY
  end
end
