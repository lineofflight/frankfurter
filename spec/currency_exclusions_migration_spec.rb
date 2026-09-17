# frozen_string_literal: true

require_relative "helper"
require "open3"
require "tmpdir"

describe "Currency exclusions migration" do
  it "indexes unknown codes already stored and remains reversible" do
    script = <<~RUBY
      require "db"
      Sequel.extension(:migration)
      Sequel::Migrator.run(DB, "db/migrate", target: 33)
      DB[:rates].multi_insert([
        { provider: "ECB", date: "2000-01-03", base: "USD", quote: "SDR", mid: 3.0 },
        { provider: "ECB", date: "2000-01-04", base: "SDR", quote: "EUR", mid: 0.3 },
        { provider: "BOC", date: "2000-01-05", base: "USD", quote: "EUR", mid: 0.9 },
        { provider: "BOC", date: "2000-01-05", base: "USD", quote: "GHC", mid: 2.0 },
        { provider: "BOC", date: "2000-01-05", base: "GHC", quote: "USD", mid: 0.5 },
      ])
      Sequel::Migrator.run(DB, "db/migrate")
      abort "missing exclusions table" unless DB.table_exists?(:currency_exclusions)
      rows = DB[:currency_exclusions].all
      abort "wrong exclusions" unless rows.size == 1 && rows.first[:iso_code] == "SDR"
      abort "wrong dates" unless rows.first.values_at(:start_date, :end_date).map(&:to_s) == ["2000-01-03", "2000-01-04"]
      Sequel::Migrator.run(DB, "db/migrate", target: 33)
      abort "rollback failed" if DB.table_exists?(:currency_exclusions)
      abort "lost rates" unless DB[:rates].count == 5
      DB.disconnect
    RUBY
    Dir.mktmpdir do |dir|
      output, status = Open3.capture2e(
        { "DATABASE_URL" => "sqlite://#{File.join(dir, "migration.sqlite3")}", "APP_ENV" => "test" },
        RbConfig.ruby, "-Ilib", "-r./boot", "-e", script,
      )

      _(status.success?).must_equal(true, output)
    end
  end
end
