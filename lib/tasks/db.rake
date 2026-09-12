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

  desc "Purge stored rates that violate ingest rules (future-dated and defunct-currency rows)"
  task :purge_invalid do
    require "blended_rate"
    require "blended_weekly_rate"
    require "blended_monthly_rate"
    require "cache"
    require "db"
    require "log"
    require "rate_validation"

    totals = RateValidation.purge(DB)
    Log.info("purge_invalid: deleted #{totals[:rates]} rates, " \
             "#{totals[:weekly_rates]} weekly, #{totals[:monthly_rates]} monthly")
    next if totals.values.sum.zero?

    # Grouped invalidation is bucket-local. Repair those gaps before the slower daily rebuild, and purge even if a
    # rebuild fails: the source deletion already committed and must not leave old responses cached.
    begin
      [BlendedWeeklyRate, BlendedMonthlyRate].each(&:populate)
      BlendedRate.rebuild
    ensure
      Cache.purge
    end
  end
end
