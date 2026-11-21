# frozen_string_literal: true

require_relative "helper"
require "open3"
require "tmpdir"

describe "Weekly buckets migration" do
  it "rekeys weekly rollups to ISO Mondays, pools default datasets, and clears stale weekly blends" do
    Dir.mktmpdir do |dir|
      script = <<~'RUBY'
        require "db"
        Sequel.extension(:migration)
        Sequel::Migrator.run(DB, "db/migrate", target: 53)

        DB[:rates].multi_insert([
          { dataset_key: "ECB", date: "2025-12-29", base: "EUR", quote: "USD", mid: 1.16 },
          { dataset_key: "ECB", date: "2026-01-02", base: "EUR", quote: "USD", mid: 1.20 },
          { dataset_key: "NBP.A", date: "2022-03-08", base: "RUB", quote: "PLN", mid: 0.03 },
          { dataset_key: "NBP.B", date: "2022-03-09", base: "RUB", quote: "PLN", mid: 0.05 },
          { dataset_key: "TCMB.FOREX", date: "2026-01-02", base: "USD", quote: "TRY", mid: 40.0 },
          { dataset_key: "TCMB.BANKNOTE", date: "2026-01-02", base: "USD", quote: "TRY", mid: 41.0 },
        ])
        DB[:weekly_rates].multi_insert([
          { dataset_key: "ECB", bucket_date: "2025-12-31", base: "EUR", quote: "USD", rate: 1.16 },
          { dataset_key: "ECB", bucket_date: "2026-01-01", base: "EUR", quote: "USD", rate: 1.20 },
        ])
        DB[:blended_weekly_rates].multi_insert([
          { bucket_date: "2025-12-31", quote: "EUR", rate: 0.86 },
          { bucket_date: "2026-01-01", quote: "EUR", rate: 0.83 },
        ])

        Sequel::Migrator.run(DB, "db/migrate", target: 54)

        abort "stale weekly blends kept" unless DB[:blended_weekly_rates].empty?
        rows = DB[:weekly_rates].order(:bucket_date, :dataset_key, :base)
          .select_map([:bucket_date, :dataset_key, :base, :quote, :rate])
          .map { |d, k, b, q, r| [d.to_s, k, b, q, r.round(6)] }
        expected = [
          ["2022-03-07", "NBP.B", "RUB", "PLN", 0.04],
          ["2025-12-29", "ECB", "EUR", "USD", 1.18],
          ["2025-12-29", "TCMB.BANKNOTE", "USD", "TRY", 41.0],
          ["2025-12-29", "TCMB.FOREX", "USD", "TRY", 40.0],
        ]
        abort "unexpected weekly_rates: #{rows.inspect}" unless rows == expected

        require "provider"
        require "rake"
        load "lib/tasks/rollups.rake"
        Provider.seed
        Cache.define_singleton_method(:purge) {}
        rebuild_rollups(DB[:rates])
        rebuilt = DB[:weekly_rates].order(:bucket_date, :dataset_key, :base)
          .select_map([:bucket_date, :dataset_key, :base, :quote, :rate])
          .map { |d, k, b, q, r| [d.to_s, k, b, q, r.round(6)] }
        abort "rebuild diverged: #{rebuilt.inspect}" unless rebuilt == expected

        Sequel::Migrator.run(DB, "db/migrate", target: 53)
        rolled_back = DB[:weekly_rates].where(dataset_key: "ECB").order(:bucket_date)
          .select_map([:bucket_date, :rate]).map { |d, r| [d.to_s, r.round(6)] }
        abort "rollback wrong: #{rolled_back.inspect}" unless rolled_back == [["2025-12-31", 1.16], ["2026-01-01", 1.2]]
        DB.disconnect
      RUBY

      output, status = Open3.capture2e(
        { "DATABASE_URL" => "sqlite://#{dir}/migration.sqlite3", "APP_ENV" => "test" },
        RbConfig.ruby, "-Ilib", "-r./boot", "-e", script,
      )

      _(status.success?).must_equal(true, output)
    end
  end
end
