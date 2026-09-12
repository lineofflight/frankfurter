# frozen_string_literal: true

require_relative "helper"
require "open3"
require "tmpdir"

describe "Grouped maintenance writer lock" do
  it "excludes concurrent writes before reading affected source buckets" do
    # A separate process avoids the suite's enclosing rollback transaction. Two real SQLite connections reproduce the
    # WAL snapshot-upgrade race; a writer transaction must already exclude the second connection's insert.
    script = <<~RUBY
      require "db"
      Sequel.extension(:migration)
      Sequel::Migrator.run(DB, "db/migrate")
      require "provider"
      require "blended_weekly_rate"
      require "blended_monthly_rate"
      require "cache"
      require "rake"
      load "lib/tasks/rollups.rake"
      Provider.seed
      Cache.define_singleton_method(:purge) {}
      row = {provider: "ECB", date: Date.new(2024, 1, 1), base: "USD", quote: "EUR", mid: 0.8}
      DB[:rates].insert(row)
      writer = Sequel.connect(ENV.fetch("DATABASE_URL"), after_connect: proc { |connection|
        connection.busy_handler_timeout = 1
        RateComponents.register(connection)
      })
      attempted = false
      blocked = false
      logger = Object.new
      logger.define_singleton_method(:error) { |message| warn message }
      logger.define_singleton_method(:info) do |sql|
        next if attempted || !sql.include?("SELECT") || !sql.include?("FROM `weekly_rates`")

        attempted = true
        begin
          writer[:rates].insert(row.merge(date: Date.new(2024, 1, 2)))
        rescue Sequel::DatabaseError => error
          raise unless error.wrapped_exception.is_a?(SQLite3::BusyException)

          blocked = true
        end
      end
      DB.loggers << logger
      recompute = BlendedWeeklyRate.method(:refresh)
      BlendedWeeklyRate.define_singleton_method(:refresh) do |dates|
        abort "source rollups were not committed before recomputation" if writer[:weekly_rates].empty?
        # This second connection can write only after the source transaction released its lock.
        writer[:rates].insert(row.merge(date: Date.new(2024, 1, 2)))
        recompute.call(dates)
      end
      rebuild_rollups(DB[:rates].where(provider: "ECB"), "ECB")
      abort "source read was not protected by the writer lock" unless attempted && blocked
      abort "missing grouped output" if BlendedWeeklyRate.empty?
      writer.disconnect
      DB.disconnect
    RUBY

    Dir.mktmpdir do |dir|
      output, status = Open3.capture2e(
        { "DATABASE_URL" => "sqlite://#{dir}/maintenance.sqlite3", "APP_ENV" => "test" },
        RbConfig.ruby, "-Ilib", "-r./boot", "-e", script,
      )

      _(status.success?).must_equal(true, output)
    end
  end
end
