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

  it "installs from scratch" do
    run_script(<<~RUBY)
      require "rake"
      load "lib/tasks/db.rake"
      Rake::Task["db:setup"].invoke
      require "provider"
      abort "no datasets" unless Dataset.count == Provider.count && Provider["ECB"].primary_dataset.key == "ECB"
      DB.disconnect
    RUBY
  end
end
