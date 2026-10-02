# frozen_string_literal: true

require_relative "helper"
require "open3"
require "tmpdir"

describe "Unknown archive codes migration" do
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
      initialize = 'require "db"; Sequel.extension(:migration); Sequel::Migrator.run(DB, "db/migrate", target: 48)'
      output, status = Open3.capture2e(env, RbConfig.ruby, "-Ilib", "-r./boot", "-e", initialize)

      _(status.success?).must_equal(true, output)
      output, status = Open3.capture2e(env, RbConfig.ruby, "-Ilib", "-r./boot", "-e", setup + script)

      _(status.success?).must_equal(true, output)
    end
  end

  it "drops the interest rate, relabels the rest and leaves the blends to a rebuild" do
    run_migration_script(<<~'RUBY')
      rows = [
        { provider: "AMCM", date: "1994-03-31", base: "LIQ", quote: "MOP", mid: 3.125 },
        { provider: "AMCM", date: "1994-03-31", base: "USD", quote: "MOP", mid: 7.985 },
        { provider: "CNB", date: "1991-01-24", base: "BEC", quote: "CZK", mid: 0.8899 },
        { provider: "CNB", date: "1991-01-24", base: "LUF", quote: "CZK", mid: 0.8899 },
        { provider: "CNB", date: "1991-01-25", base: "BEF", quote: "CZK", mid: 0.8931 },
        { provider: "CNB", date: "1991-01-03", base: "YUD", quote: "CZK", mid: 2.04 },
        { provider: "CNB", date: "1993-03-10", base: "XCU", quote: "CZK", mid: 33.436 },
        { provider: "CNB", date: "1993-03-10", base: "XEU", quote: "CZK", mid: 34.118 },
        { provider: "NBU", date: "2000-09-01", base: "TJR", quote: "UAH", mid: 0.0027776 },
        { provider: "NBU", date: "2000-11-01", base: "ZAL", quote: "UAH", mid: 2.4713 },
        { provider: "NBU", date: "2002-11-01", base: "ZAL", quote: "UAH", mid: 1.8052 },
        { provider: "NBU", date: "2002-12-02", base: "TJS", quote: "UAH", mid: 1.805263 },
      ]
      DB[:rates].multi_insert(rows)
      CurrencySummary.refresh(DB, rows.flat_map { |row| row.values_at(:base, :quote) })
      # As stored before XCU was registered.
      DB[:currency_coverages].where(iso_code: "XCU").delete
      DB[:currencies].where(iso_code: "XCU").delete
      DB[:currency_exclusions].insert(
        provider_key: "CNB", iso_code: "XCU", start_date: "1993-03-10", end_date: "1993-03-10",
      )
      [:weekly_rates, :monthly_rates].zip([Bucket.week, Bucket.month]).each do |table, bucket|
        DB[table].insert(
          [:bucket_date, :provider, :base, :quote, :rate],
          DB[:rates].select(bucket, :provider, :base, :quote, Sequel.function(:avg, :rate))
            .group(bucket, :provider, :base, :quote),
        )
      end
      DB[:blended_rates].insert(date: "2000-11-01", quote: "TJS", rate: 2.2)
      DB[:rate_spikes].insert(provider: "AMCM", date: "1994-03-31", base: "LIQ", quote: "MOP")
      unknown = ->(key) { Provider[key].unknown_currencies }
      abort "wrong setup: #{unknown["CNB"]}" unless unknown["CNB"] == ["BEC", "YUD"] && unknown["NBU"] == ["ZAL"]
      abort "wrong setup: #{unknown["AMCM"]}" unless unknown["AMCM"] == ["LIQ"]
      blends = [BlendedRate, BlendedWeeklyRate, BlendedMonthlyRate]
      snapshot = -> { blends.to_h { |model| [model, model.dataset.order(*model.primary_key).all] } }
      before = snapshot.call

      require "rake"
      load "lib/tasks/db.rake"
      Rake::Task["db:setup"].invoke
      abort "migration incomplete" unless DB[:schema_info].get(:version) >= 49

      stored = DB[:rates].order(:provider, :date, :base).select_map([:provider, :date, :base, :quote, :mid])
        .map { |provider, date, base, quote, mid| [provider, date.to_s, base, quote, mid] }
      expected = [
        ["AMCM", "1994-03-31", "USD", "MOP", 7.985],
        ["CNB", "1991-01-03", "YUN", "CZK", 2.04],
        ["CNB", "1991-01-24", "BEF", "CZK", 0.8899],
        ["CNB", "1991-01-24", "LUF", "CZK", 0.8899],
        ["CNB", "1991-01-25", "BEF", "CZK", 0.8931],
        ["CNB", "1993-03-10", "XCU", "CZK", 33.436],
        ["CNB", "1993-03-10", "XEU", "CZK", 34.118],
        ["NBU", "2000-09-01", "TJR", "UAH", 0.0027776],
        ["NBU", "2000-11-01", "TJS", "UAH", 2.4713],
        ["NBU", "2002-11-01", "TJS", "UAH", 1.8052],
        ["NBU", "2002-12-02", "TJS", "UAH", 1.805263],
      ]
      abort "wrong repaired rows: #{stored.inspect}" unless stored == expected

      weekly = ->(provider, base) { DB[:weekly_rates].where(provider:, base:).order(:bucket_date).select_map(:rate) }
      abort "rollup kept LIQ" unless weekly["AMCM", "LIQ"].empty? && weekly["AMCM", "USD"] == [7.985]
      abort "rollup kept BEC" unless weekly["CNB", "BEC"].empty? && weekly["CNB", "BEF"].map { it.round(6) } == [0.8915]
      abort "rollup kept YUD" unless weekly["CNB", "YUD"].empty? && weekly["CNB", "YUN"] == [2.04]
      abort "rollup kept ZAL" unless weekly["NBU", "ZAL"].empty? && weekly["NBU", "TJS"] == [2.4713, 1.8052, 1.805263]
      monthly = DB[:monthly_rates].where(provider: "NBU", base: "TJS").order(:bucket_date).select_map(:rate)
      abort "monthly rollup: #{monthly}" unless monthly == [2.4713, 1.8052, 1.805263]

      coverage = ->(provider, code) do
        DB[:currency_coverages].where(provider_key: provider, iso_code: code).select_map([:start_date, :end_date])
          .map { |dates| dates.map(&:to_s) }
      end
      abort "BEF coverage" unless coverage["CNB", "BEF"] == [["1991-01-24", "1991-01-25"]]
      abort "YUN coverage" unless coverage["CNB", "YUN"] == [["1991-01-03", "1991-01-03"]]
      abort "XCU coverage" unless coverage["CNB", "XCU"] == [["1993-03-10", "1993-03-10"]]
      abort "TJS coverage" unless coverage["NBU", "TJS"] == [["2000-11-01", "2002-12-02"]]
      abort "XCU missing from the catalogue" unless DB[:currencies].where(iso_code: "XCU").count == 1
      abort "kept unknown codes" unless DB[:currency_exclusions].where(provider_key: ["AMCM", "CNB", "NBU"]).empty?
      ["AMCM", "CNB", "NBU"].each do |key|
        abort "#{key} still fails provider health" unless unknown[key].empty?
      end

      abort "kept a stale spike flag" unless DB[:rate_spikes].where(provider: "AMCM").empty?
      abort "touched the blends" unless snapshot.call == before
      DB.disconnect
    RUBY
  end

  it "rolls back on conflicting duplicates" do
    run_migration_script(<<~RUBY)
      DB[:rates].multi_insert([
        { provider: "AMCM", date: "1994-03-31", base: "LIQ", quote: "MOP", mid: 3.125 },
        { provider: "NBU", date: "2000-11-01", base: "ZAL", quote: "UAH", mid: 2.4713 },
        { provider: "NBU", date: "2000-11-01", base: "TJS", quote: "UAH", mid: 2.5 },
      ])
      DB[:blended_rates].insert(date: "2000-11-01", quote: "TJS", rate: 2.2)
      before = DB[:rates].order(:provider, :base).all
      begin
        Sequel::Migrator.run(DB, "db/migrate")
        abort "accepted conflicting components"
      rescue RuntimeError => error
        raise unless error.message.include?("conflicting ZAL/TJS components")
      end
      abort "partial repair committed" unless DB[:rates].order(:provider, :base).all == before
      abort "migration marked complete" unless DB[:schema_info].get(:version) == 48
      abort "touched the blends" unless DB[:blended_rates].count == 1
      DB.disconnect
    RUBY
  end

  it "leaves databases without affected rows alone" do
    run_migration_script(<<~RUBY)
      DB[:rates].multi_insert([
        { provider: "AMCM", date: "1994-03-31", base: "USD", quote: "MOP", mid: 7.985 },
        { provider: "CNB", date: "1991-01-25", base: "BEF", quote: "CZK", mid: 0.8931 },
        { provider: "CNB", date: "1993-03-10", base: "XCU", quote: "CZK", mid: 33.436 },
        { provider: "NBU", date: "2002-12-02", base: "TJS", quote: "UAH", mid: 1.805263 },
        { provider: "LB", date: "1996-05-07", base: "YUM", quote: "LTL", mid: 0.7935 },
      ])
      DB[:blended_rates].insert(date: "2002-12-02", quote: "TJS", rate: 3.0)
      before = DB[:rates].order(:provider, :base).all
      Sequel::Migrator.run(DB, "db/migrate")
      abort "migration incomplete" unless DB[:schema_info].get(:version) >= 49
      abort "changed unaffected rows" unless DB[:rates].order(:provider, :base).all == before
      abort "touched the blends" unless DB[:blended_rates].count == 1
      DB.disconnect
    RUBY
  end
end
