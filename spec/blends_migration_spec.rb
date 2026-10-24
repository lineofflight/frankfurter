# frozen_string_literal: true

require_relative "helper"
require "open3"
require "tmpdir"

describe "Blends migration" do
  # db:setup migrates and seeds in one process, and a pending migration that loads Provider caches its columns before
  # the blends column exists.
  it "seeds the flag in the process that adds the column" do
    initialize = 'require "db"; Sequel.extension(:migration); Sequel::Migrator.run(DB, "db/migrate", target: 49)'
    script = <<~RUBY
      require "db"
      Sequel.extension(:migration)
      require "provider"
      Provider.columns
      Sequel::Migrator.run(DB, "db/migrate")
      Provider.seed
      abort "ESTV blends" if Provider["ESTV"].blends?
      abort "ECB does not blend" unless Provider["ECB"].blends?
    RUBY
    Dir.mktmpdir do |dir|
      env = { "DATABASE_URL" => "sqlite://#{File.join(dir, "migration.sqlite3")}", "APP_ENV" => "test" }
      output, status = Open3.capture2e(env, RbConfig.ruby, "-Ilib", "-r./boot", "-e", initialize)

      _(status.success?).must_equal(true, output)
      output, status = Open3.capture2e(env, RbConfig.ruby, "-Ilib", "-r./boot", "-e", script)

      _(status.success?).must_equal(true, output)
    end
  end
end
