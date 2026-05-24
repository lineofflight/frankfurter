# frozen_string_literal: true

desc "Migrate and seed the database"
task "db:setup" => ["db:migrate", "db:seed"]

namespace :db do
  desc "Run database migrations"
  task :migrate do
    require "db"

    Sequel.extension(:migration)
    db = Sequel::DATABASES.first
    dir = File.expand_path("../../db/migrate", __dir__)
    opts = {}
    opts.update(target: ENV["VERSION"].to_i) if ENV["VERSION"]

    Sequel::IntegerMigrator.new(db, dir, opts).run
  end

  desc "Seed database from saved data"
  task :seed do
    require "provider"
    Provider.seed
  end

  desc "Purge rates for defunct currency codes past their terminal date"
  task :purge_obsolete do
    require "currency_terminal_date"
    require "db"
    require "log"

    totals = CurrencyTerminalDate.purge(DB)
    Log.info("purge_obsolete: deleted #{totals[:rates]} rates, " \
      "#{totals[:weekly_rates]} weekly, #{totals[:monthly_rates]} monthly")
  end
end
