# frozen_string_literal: true

require_relative "helper"
require "open3"
require "tmpdir"

describe "Rate component migrations" do
  it "keeps rates readable through rollback and reapplication without a custom SQLite function" do
    script = <<~RUBY
      require "db"
      Sequel.extension(:migration)
      Sequel::Migrator.run(DB, "db/migrate")
      identity = {provider: "TST", date: Date.new(2026, 9, 1), base: "USD"}
      DB[:rates].multi_insert([
        identity.merge(quote: "EUR", mid: 100, bid: 99, ask: 103),
        identity.merge(quote: "GBP", bid: 1830.59054685, ask: 1831.5063),
        identity.merge(provider: "BOJA", quote: "JPY", bid: 0, ask: 103),
      ])
      expected = DB[:rates].order(:quote).select_map(:rate)

      [32, 30, 33].each do |version|
        Sequel::Migrator.run(DB, "db/migrate", target: version)
        abort "wrong migration version" unless DB[:schema_info].get(:version) == version
        # A standalone connection must read the schema without application callbacks.
        SQLite3::Database.new(ENV.fetch("SNAPSHOT_PATH")) do |connection|
          actual = connection.execute("SELECT rate FROM rates ORDER BY quote").flatten
          abort "rates changed at version \#{version}" unless actual == expected
        end
      end
      DB.disconnect
    RUBY

    Dir.mktmpdir do |dir|
      path = File.join(dir, "migration.sqlite3")
      output, status = Open3.capture2e(
        { "DATABASE_URL" => "sqlite://#{path}", "SNAPSHOT_PATH" => path, "APP_ENV" => "test" },
        RbConfig.ruby, "-Ilib", "-r./boot", "-e", script,
      )

      _(status.success?).must_equal(true, output)
    end
  end
end
