# frozen_string_literal: true

desc "Migrate and seed the database"
task "db:setup" => ["db:migrate", "db:seed"]

namespace :db do
  desc "Run database migrations"
  task :migrate do
    require "db"

    Sequel.extension(:migration)
    db = Sequel::DATABASES.first
    # Migrations up to 049 repair rates in place and leave what derives from them to one rebuild after the last
    # migration. The marker outlives a failed run, so the next boot finishes the job.
    version = db.table_exists?(:schema_info) ? db[:schema_info].get(:version).to_i : 0
    if version < 49 && db.table_exists?(:rates) && !db[:rates].empty?
      db.create_table?(:derived_rebuild_owed) { Integer :since }
      db[:derived_rebuild_owed].insert(since: version) if db[:derived_rebuild_owed].empty?
    end

    dir = File.expand_path("../../db/migrate", __dir__)
    opts = {}
    opts.update(target: ENV["VERSION"].to_i) if ENV["VERSION"]

    Sequel::IntegerMigrator.new(db, dir, opts).run
    next if ENV["VERSION"] || !db.table_exists?(:derived_rebuild_owed)

    # The rebuild reads dataset metadata, so seed first.
    Rake::Task["db:seed"].invoke
    Rake::Task["db:rebuild"].invoke
  end

  desc "Rebuild currency coverage, spike flags, rollups and blends from rates"
  task :rebuild do
    require "derived_tables"

    DerivedTables.rebuild
    DB.drop_table?(:derived_rebuild_owed)
  end

  desc "Seed database from saved data"
  task :seed do
    require "provider"
    Provider.seed
  end

  desc "Purge stored rates beyond each provider's future-date horizon"
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
