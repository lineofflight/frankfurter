# frozen_string_literal: true

# Records the schema Sequel's migrator leaves at every version, up from 0 to the latest and back down until the first
# irreversible migration, so the Go migrations can be checked against it version by version.
#
#   DATABASE_URL=sqlite://$SCRATCH/schemas.sqlite3 APP_ENV=test \
#     mise exec -- bundle exec ruby -Ilib -r./boot go/scripts/migration_schemas.rb \
#     go/internal/migrate/testdata/schemas.json
#
# The database must be a throwaway: the script migrates it from scratch.

require "db"
require "json"

Sequel.extension(:migration)

abort "database is not empty" unless DB.tables.empty?

dir = File.expand_path("../../db/migrate", __dir__)
latest = Sequel::IntegerMigrator.new(DB, dir).send(:latest_migration_version)

def schema
  DB.fetch("SELECT type, name, tbl_name, sql FROM sqlite_master ORDER BY name").all.map do |row|
    row.transform_keys(&:to_s)
  end
end

up = {}
(1..latest).each do |version|
  Sequel::IntegerMigrator.new(DB, dir, target: version).run
  up[version] = schema
end

down = {}
(latest - 1).downto(0) do |version|
  Sequel::IntegerMigrator.new(DB, dir, target: version).run
  down[version] = schema
rescue Sequel::Error => e
  down["irreversible"] = { "version" => version + 1, "message" => e.message }
  break
end

File.write(ARGV.fetch(0), JSON.pretty_generate("latest" => latest, "up" => up, "down" => down) + "\n")
