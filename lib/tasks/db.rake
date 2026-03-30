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
end
