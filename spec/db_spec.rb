# frozen_string_literal: true

require_relative "helper"

describe "Database connection configuration" do
  it "appends TEST_ENV_NUMBER to test database name when set" do
    output = `APP_ENV=test TEST_ENV_NUMBER=3 bundle exec ruby -Ilib -e 'require "db"; puts DB.opts[:database]'`

    _(output.strip).must_equal(File.expand_path("db/frankfurter_test_3.sqlite3"))
  end

  it "does not append suffix when TEST_ENV_NUMBER is empty" do
    output = `APP_ENV=test TEST_ENV_NUMBER= bundle exec ruby -Ilib -e 'require "db"; puts DB.opts[:database]'`

    _(output.strip).must_equal(File.expand_path("db/frankfurter_test.sqlite3"))
  end

  it "does not append suffix when TEST_ENV_NUMBER is unset" do
    output = `env -u TEST_ENV_NUMBER APP_ENV=test bundle exec ruby -Ilib -e 'require "db"; puts DB.opts[:database]'`

    _(output.strip).must_equal(File.expand_path("db/frankfurter_test.sqlite3"))
  end

  it "uses DATABASE_URL when set, ignoring TEST_ENV_NUMBER" do
    cmd = "APP_ENV=test TEST_ENV_NUMBER=3 DATABASE_URL=sqlite://custom.sqlite3 " \
          "bundle exec ruby -Ilib -e 'require \"db\"; puts DB.opts[:database]'"
    output = `#{cmd}`

    _(output.strip).must_equal("custom.sqlite3")
  end
end
