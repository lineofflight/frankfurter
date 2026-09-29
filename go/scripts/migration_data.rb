# frozen_string_literal: true

# Records what Sequel's migrator does to data: loads each go/internal/migrate/testdata/data/phase_vN.sql once the
# database reaches version N, migrates up to the latest, dumps every table, then rolls back to 8 (008 refuses to go
# further) and dumps again. The Go migrations replay the same phases and must leave the same rows.
#
#   DATABASE_URL=sqlite://$SCRATCH/data.sqlite3 APP_ENV=test \
#     mise exec -- bundle exec ruby -Ilib -r./boot go/scripts/migration_data.rb \
#     go/internal/migrate/testdata/data
#
# The database must be a throwaway: the script migrates it from scratch.

require "db"
require "json"

Sequel.extension(:migration)

abort "database is not empty" unless DB.tables.empty?

out = ARGV.fetch(0)
dir = File.expand_path("../../db/migrate", __dir__)
latest = Sequel::IntegerMigrator.new(DB, dir).send(:latest_migration_version)
phases = Dir[File.join(out, "phase_v*.sql")].to_h { |f| [Integer(File.basename(f)[/\d+/]), f] }

def dump
  DB.tables.map(&:to_s).sort.to_h do |table|
    columns = DB.fetch("SELECT name FROM pragma_table_xinfo(?)", table).map { |r| r[:name] }
    list = columns.map { |c| "`#{c}`" }.join(", ")
    rows = DB.fetch("SELECT #{list} FROM `#{table}` ORDER BY #{list}").map do |row|
      row.values.map { |v| v.is_a?(Date) ? v.to_s : v }
    end
    [table, { "columns" => columns, "rows" => rows }]
  end
end

(1..latest).each do |version|
  if version == 27
    # 027 and 028 recompute part of the daily blend with today's model code, which in one process breaks on columns
    # these old schemas lack (Provider#frequency). Go clears blended_rates instead (core-ops.md); blended_rates is empty
    # here at that point either way, so stub the recompute out.
    require "blended_rate"
    BlendedRate.define_singleton_method(:refresh) { |*| }
  end
  Sequel::IntegerMigrator.new(DB, dir, target: version).run
  next unless phases[version]

  File.read(phases[version]).split(/;\s*$/).map(&:strip).reject(&:empty?).each { |sql| DB.run(sql) }
end
up = dump
Sequel::IntegerMigrator.new(DB, dir, target: 8).run
down = dump

File.write(File.join(out, "ruby.json"), JSON.pretty_generate({ "up" => up, "down" => down }) + "\n")
