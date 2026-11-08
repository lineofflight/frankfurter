# frozen_string_literal: true

require_relative "helper"
require "open3"
require "tmpdir"

describe "Datasets migration" do
  def run_script(script)
    Dir.mktmpdir do |dir|
      output, status = Open3.capture2e(
        { "DATABASE_URL" => "sqlite://#{dir}/migration.sqlite3", "APP_ENV" => "test" },
        RbConfig.ruby, "-Ilib", "-r./boot", "-e", script,
      )

      _(status.success?).must_equal(true, output)
    end
  end

  it "moves series metadata to one default dataset per provider and renames the key columns in place" do
    run_script(<<~'RUBY')
      require "db"
      Sequel.extension(:migration)
      Sequel::Migrator.run(DB, "db/migrate", target: 49)
      DB[:providers].insert(
        key: "BOJA", name: "Bank of Jamaica", country_code: "JM", rate_type: "selling rate", pivot_currency: "JMD",
        data_url: "https://boj.org.jm", terms_url: nil, publish_schedule: "0 22 * * 1-5", publish_cadence: "daily",
        coverage_start: "2000-01-03", frequency: "daily",
      )
      DB[:rates].multi_insert([
        { provider: "BOJA", date: "2026-01-02", base: "USD", quote: "JMD", mid: nil, bid: 0, ask: 157.5 },
        { provider: "ECB", date: "2026-01-02", base: "EUR", quote: "USD", mid: 1.17, bid: nil, ask: nil },
      ])
      DB[:weekly_rates].insert(provider: "ECB", bucket_date: "2025-12-31", base: "EUR", quote: "USD", rate: 1.17)
      DB[:monthly_rates].insert(provider: "ECB", bucket_date: "2026-01-01", base: "EUR", quote: "USD", rate: 1.17)
      DB[:rate_spikes].insert(provider: "ECB", date: "2026-01-02", base: "EUR", quote: "USD")
      DB[:currency_coverages].insert(provider_key: "ECB", iso_code: "USD", start_date: "2026-01-02", end_date: "2026-01-02")
      DB[:currency_exclusions].insert(provider_key: "ECB", iso_code: "ZZZ", start_date: "2026-01-02", end_date: "2026-01-02")
      rates = DB[:rates].order(:provider).select_map([:provider, :date, :base, :quote, :rate])

      Sequel::Migrator.run(DB, "db/migrate", target: 51)

      abort "provider columns left" unless DB.schema(:providers).map(&:first) == [:key, :name, :terms_url, :country_code]
      dataset = DB[:datasets].first(key: "BOJA")
      expected = {
        key: "BOJA", provider_key: "BOJA", is_default: true, blends: nil, rate_type: "selling rate",
        frequency: "daily", publish_cadence: "daily", publish_schedule: "0 22 * * 1-5", pivot_currency: "JMD",
        data_url: "https://boj.org.jm", coverage_start: Date.new(2000, 1, 3),
      }
      abort "wrong dataset: #{dataset.inspect}" unless dataset == expected
      tables = [:rates, :weekly_rates, :monthly_rates, :rate_spikes, :currency_coverages, :currency_exclusions]
      tables.each do |table|
        abort "#{table} not renamed" unless DB.schema(table).map(&:first).include?(:dataset_key)
        abort "#{table} lost rows" unless DB[table].select_map(:dataset_key).uniq.sort == (table == :rates ? ["BOJA", "ECB"] : ["ECB"])
      end
      # The generated rate column still resolves BOJA's zero bid from its ask.
      renamed = DB[:rates].order(:dataset_key).select_map([:dataset_key, :date, :base, :quote, :rate])
      abort "rates changed: #{renamed.inspect}" unless renamed == rates
      unique = DB.indexes(:rates).values.find { |index| index[:unique] }
      abort "unique index lost" unless unique && unique[:columns] == [:dataset_key, :date, :base, :quote]

      Sequel::Migrator.run(DB, "db/migrate", target: 49)

      abort "rollback lost metadata" unless DB[:providers].first(key: "BOJA")[:publish_schedule] == "0 22 * * 1-5"
      abort "rollback kept datasets" if DB.table_exists?(:datasets)
      abort "rollback changed rates" unless DB[:rates].order(:provider).select_map([:provider, :date, :base, :quote, :rate]) == rates
      DB.disconnect
    RUBY
  end

  it "splits NBP's stored rows by table without touching a value" do
    run_script(<<~'RUBY')
      require "db"
      require "bucket"
      Sequel.extension(:migration)
      Sequel::Migrator.run(DB, "db/migrate", target: 51)
      nbp = ->(date, base, mid) { { dataset_key: "NBP", date:, base:, quote: "PLN", mid: } }
      DB[:rates].multi_insert([
        nbp["2002-01-02", "USD", 3.9],
        nbp["2002-01-02", "AED", 1.1],
        nbp["2002-12-31", "RUB", 0.12],
        nbp["2003-01-07", "RUB", 0.121],
        nbp["2022-03-07", "RUB", 0.03125],
        nbp["2022-03-08", "RUB", 0.0390625],
        nbp["2022-03-09", "RUB", 0.046875],
        nbp["2013-01-02", "XAU", 5200.0],
        nbp["2020-01-01", "ZZZ", 2.0],
        { dataset_key: "ECB", date: "2022-03-08", base: "EUR", quote: "RUB", mid: 120.0 },
      ])
      DB[:rate_spikes].multi_insert([
        { dataset_key: "NBP", date: "2022-03-08", base: "RUB", quote: "PLN" },
        { dataset_key: "NBP", date: "2022-03-09", base: "RUB", quote: "PLN" },
      ])
      { weekly_rates: Bucket.week, monthly_rates: Bucket.month }.each do |table, bucket|
        DB[table].insert(
          [:bucket_date, :dataset_key, :base, :quote, :rate],
          DB[:rates].select(bucket, :dataset_key, :base, :quote, Sequel.function(:avg, :rate))
            .group(bucket, :dataset_key, :base, :quote),
        )
      end
      [[:currency_coverages, ["USD", "AED", "RUB", "XAU", "PLN"]], [:currency_exclusions, ["ZZZ"]]].each do |table, codes|
        codes.each do |code|
          rows = DB[:rates].where(dataset_key: "NBP").where(Sequel.|({ base: code }, { quote: code }))
          DB[table].insert(dataset_key: "NBP", iso_code: code, start_date: rows.min(:date), end_date: rows.max(:date))
        end
      end
      snapshot = lambda do
        {
          rates: DB[:rates].order(:date, :base).select_map([:date, :base, :quote, :rate]),
          weekly: DB[:weekly_rates].order(:bucket_date, :base).select_map([:bucket_date, :base, :rate]),
          monthly: DB[:monthly_rates].order(:bucket_date, :base).select_map([:bucket_date, :base, :rate]),
          spikes: DB[:rate_spikes].order(:date).select_map([:date, :base]),
        }
      end
      before = snapshot.call
      coverage = DB[:currency_coverages].select_hash(:iso_code, [:start_date, :end_date])

      Sequel::Migrator.run(DB, "db/migrate", target: 52)

      abort "values changed" unless snapshot.call == before
      tables = [:rates, :rate_spikes, :weekly_rates, :monthly_rates, :currency_coverages, :currency_exclusions]
      tables.each { |table| abort "#{table} kept NBP" unless DB[table].where(dataset_key: "NBP").empty? }
      labels = DB[:rates].exclude(dataset_key: "ECB").order(:date, :base).select_map([:date, :base, :dataset_key])
        .map { |date, base, key| "#{date} #{base} #{key}" }
      expected = [
        "2002-01-02 AED NBP.B", "2002-01-02 USD NBP.A", "2002-12-31 RUB NBP.B", "2003-01-07 RUB NBP.A",
        "2013-01-02 XAU NBP.A", "2020-01-01 ZZZ NBP.B", "2022-03-07 RUB NBP.A", "2022-03-08 RUB NBP.A",
        "2022-03-09 RUB NBP.B",
      ]
      abort "wrong tables: #{labels.inspect}" unless labels == expected
      spikes = DB[:rate_spikes].order(:date).select_map([:date, :dataset_key]).map { |date, key| "#{date} #{key}" }
      abort "spikes stayed: #{spikes.inspect}" unless spikes == ["2022-03-08 NBP.A", "2022-03-09 NBP.B"]
      # RUB's March 2022 move spans one week and one month, which keep their average under the table it moved to.
      straddled = [[:weekly_rates, Bucket.week("2022-03-08")], [:monthly_rates, "2022-03-01"]].map do |table, bucket|
        DB[table].where(base: "RUB", bucket_date: DB.get(Sequel.as(bucket, :b))).select_map(:dataset_key)
      end
      abort "straddled buckets: #{straddled.inspect}" unless straddled == [["NBP.B"], ["NBP.B"]]
      split = DB[:currency_coverages].exclude(dataset_key: "ECB").order(:iso_code, :dataset_key)
        .select_map([:iso_code, :dataset_key, :start_date, :end_date]).map { |row| row.map(&:to_s).join(" ") }
      abort "wrong coverage: #{split.inspect}" unless split == [
        "AED NBP.B 2002-01-02 2002-01-02",
        "PLN NBP.A 2002-01-02 2022-03-08", "PLN NBP.B 2002-01-02 2022-03-09",
        "RUB NBP.A 2003-01-07 2022-03-08", "RUB NBP.B 2002-12-31 2022-03-09",
        "USD NBP.A 2002-01-02 2002-01-02", "XAU NBP.A 2013-01-02 2013-01-02",
      ]
      merged = DB[:currency_coverages].exclude(dataset_key: "ECB").group(:iso_code)
        .select_hash(:iso_code, [Sequel.function(:min, :start_date).as(:s), Sequel.function(:max, :end_date).as(:e)])
      abort "coverage moved" unless merged.transform_values { |v| v.map(&:to_s) } == coverage.transform_values { |v| v.map(&:to_s) }
      abort "exclusion stayed" unless DB[:currency_exclusions].select_map([:dataset_key, :iso_code]) == [["NBP.B", "ZZZ"]]

      # Rebuilding NBP's rollups pools the two tables again, so it reproduces what the migration stored.
      require "provider"
      require "rake"
      load "lib/tasks/rollups.rake"
      Provider.seed
      Cache.define_singleton_method(:purge) {}
      rollups = lambda do
        [:weekly_rates, :monthly_rates].to_h do |table|
          [table, DB[table].order(:bucket_date, :base).select_map([:bucket_date, :dataset_key, :base, :rate])]
        end
      end
      stored = rollups.call
      rebuild_rollups(DB[:rates].where(dataset_key: ["NBP.A", "NBP.B"]), "NBP", ["NBP.A", "NBP.B"])
      abort "rebuild changed rollups: #{rollups.call.inspect}" unless rollups.call == stored
      DB.disconnect
    RUBY
  end

  it "refuses to migrate a populated database older than 049 and leaves it alone" do
    Dir.mktmpdir do |dir|
      env = { "DATABASE_URL" => "sqlite://#{dir}/old.sqlite3", "APP_ENV" => "test" }
      setup = <<~RUBY
        require "db"
        Sequel.extension(:migration)
        Sequel::Migrator.run(DB, "db/migrate", target: 48)
        DB[:rates].insert(provider: "ECB", date: "2026-01-02", base: "EUR", quote: "USD", mid: 1.17)
      RUBY
      output, status = Open3.capture2e(env, RbConfig.ruby, "-Ilib", "-r./boot", "-e", setup)

      _(status.success?).must_equal(true, output)

      output, status = Open3.capture2e(env, "bundle", "exec", "rake", "db:migrate")

      _(status.success?).must_equal(false)
      _(output).must_include("v2.6.1")
      check = 'require "db"; print DB[:schema_info].get(:version), " ", DB[:rates].count'
      output, = Open3.capture2e(env, RbConfig.ruby, "-Ilib", "-r./boot", "-e", check)

      _(output).must_equal("48 1")
    end
  end

  it "installs from scratch" do
    run_script(<<~RUBY)
      require "rake"
      load "lib/tasks/db.rake"
      Rake::Task["db:setup"].invoke
      require "provider"
      abort "no datasets" unless Provider.all.all?(&:primary_dataset) && Provider["ECB"].primary_dataset.key == "ECB"
      abort "NBP not split" unless Provider["NBP"].default_datasets.map(&:key) == ["NBP.A", "NBP.B"]
      DB.disconnect
    RUBY
  end
end
