# frozen_string_literal: true

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

  desc "Run database migrations and backfill all providers"
  task prepare: ["db:migrate", "ecb:backfill", "boc:backfill", "tcmb:backfill"]

  namespace :test do
    desc "Run database migrations and seed with saved data"
    task :prepare do
      Rake::Task["db:migrate"].invoke
      Rake::Task["ecb:seed"].invoke
    end
  end
end
