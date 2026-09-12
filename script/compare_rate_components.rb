# frozen_string_literal: true

# Compare and enrich a migrated LOCAL snapshot. Never point this script at the production database.
require_relative "../boot"
require "rate_components"
require "sqlite3"
require "json"

baseline, candidate, *sources = ARGV
materialized_tables = [:weekly_rates, :monthly_rates, :blended_rates, :blended_weekly_rates, :blended_monthly_rates]
abort "Usage: ruby script/compare_rate_components.rb BASELINE CANDIDATE COMPONENTS.jsonl [...]" if sources.empty?
abort "Baseline and candidate must differ" if File.realpath(baseline) == File.realpath(candidate)
connection = SQLite3::Database.new(candidate)
RateComponents.register(connection)
connection.execute("ATTACH DATABASE ? AS baseline", ["file:#{File.expand_path(baseline)}?mode=ro"])
schema = connection.get_first_value("SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'rates'")
abort "Candidate must have the component migration" unless schema.include?("frankfurter_midpoint")
connection.execute(schema.sub(/CREATE TABLE ["`]?rates["`]?/i, "CREATE TEMP TABLE incoming"))
connection.execute("ALTER TABLE incoming ADD COLUMN expected REAL")
connection.transaction do
  insert = connection.prepare(<<~SQL)
    INSERT INTO incoming (provider, date, base, quote, mid, bid, ask, expected)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?)
  SQL
  sources.each do |source|
    File.foreach(source) do |line|
      record = JSON.parse(line, symbolize_names: true)
      values = RateComponents.attributes(record)
      insert.execute(*values.values_at(:provider, :date, :base, :quote, :mid, :bid, :ask),
                     RatePrecision.normalize(record[:rate]),)
    end
  end
  insert.close
end
connection.execute("CREATE INDEX incoming_identity ON incoming (provider, date, base, quote)")
identity = "r.provider = i.provider AND r.date = i.date AND r.base = i.base AND r.quote = i.quote"
report = {}
report[:observations] = connection.get_first_value("SELECT COUNT(*) FROM incoming")
report[:calculation_differences] =
  connection.get_first_value("SELECT COUNT(*) FROM incoming WHERE rate IS NOT expected")
report[:source_differences] =
  connection.get_first_value(<<~SQL)
    SELECT COUNT(*) FROM incoming i JOIN baseline.rates r ON #{identity} WHERE i.expected IS NOT r.rate
  SQL
report[:not_stored] =
  connection.get_first_value(<<~SQL)
    SELECT COUNT(*) FROM incoming i WHERE NOT EXISTS (SELECT 1 FROM baseline.rates r WHERE #{identity})
  SQL
connection.results_as_hash = true
report[:calculation_examples] = connection.execute("SELECT * FROM incoming WHERE rate IS NOT expected LIMIT 10")
report[:source_examples] =
  connection.execute(<<~SQL)
    SELECT i.*, r.rate AS stored FROM incoming i JOIN baseline.rates r ON #{identity}
    WHERE i.expected IS NOT r.rate LIMIT 10
  SQL
connection.transaction do
  connection.execute(<<~SQL)
    UPDATE rates AS r SET mid = i.mid, bid = i.bid, ask = i.ask
    FROM incoming AS i
    WHERE #{identity} AND r.rate IS i.expected AND i.rate IS i.expected
      AND (i.bid IS NOT NULL OR i.ask IS NOT NULL)
  SQL
  report[:enriched_rows] = connection.changes
end
puts JSON.pretty_generate(report)
$stdout.flush

report[:rate_count] = connection.get_first_value("SELECT COUNT(*) FROM rates")
report[:baseline_rate_count] = connection.get_first_value("SELECT COUNT(*) FROM baseline.rates")
report[:rate_differences] = connection.get_first_value(<<~SQL)
  SELECT COUNT(*) FROM rates r LEFT JOIN baseline.rates b
    ON r.provider = b.provider AND r.date = b.date AND r.base = b.base AND r.quote = b.quote
  WHERE b.provider IS NULL OR r.rate IS NOT b.rate
SQL
materialized_tables.each do |table|
  count = connection.get_first_value("SELECT COUNT(*) FROM #{table}")
  baseline_count = connection.get_first_value("SELECT COUNT(*) FROM baseline.#{table}")
  differences = connection.get_first_value(<<~SQL)
    SELECT COUNT(*) FROM (SELECT * FROM #{table} EXCEPT SELECT * FROM baseline.#{table})
  SQL
  report[table] = { count:, baseline_count:, differences: }
end
puts JSON.pretty_generate(report)
abort "Calculation parity failed" unless report[:calculation_differences].zero?
snapshot_matches = report[:rate_differences].zero? && report[:rate_count] == report[:baseline_rate_count]
abort "Snapshot parity failed" unless snapshot_matches
materialized_match = materialized_tables.all? do |table|
  report[table][:differences].zero? && report[table][:count] == report[table][:baseline_count]
end
abort "Materialized table parity failed" unless materialized_match
