# frozen_string_literal: true

# Exploratory comparison: retain the unchanged adapter rate as the oracle and independently resolve its source
# components in SQLite. Run with APP_ENV=test after db:setup; VCR replay never accesses the network.
require_relative "../boot"
require "db"
require "provider/adapters"
require "rate_precision"
require "vcr"
require "webmock"
require "json"
require "sqlite3"
require "fileutils"

VCR.configure do |config|
  config.cassette_library_dir = "spec/vcr_cassettes"
  config.hook_into(:webmock)
  config.default_cassette_options = { record: :none, allow_playback_repeats: true }
end

["TCMB", "BAM"].each { |key| ENV["#{key}_API_KEY"] ||= "test" }

CASES = {
  "CBE" => ["2026-04-01", "2026-04-09", [:method, :host]],
  "CBTT" => ["2026-03-02", "2026-03-06", [:method, :host]],
  "BOJA" => [nil, nil, [:method, :host]],
  "BOZ" => ["2006-01-01", "2026-09-08", [:method, :host, :path]],
  "TCMB" => ["2026-03-01", "2026-03-22", [:method, :host]],
  "CBO" => ["2026-06-01", "2026-06-04", [:method, :uri, :body]],
  "CBLLR" => ["2026-05-15", "2026-05-22", [:method, :host]],
  "BCRP" => ["2026-09-01", "2026-09-08", [:method, :uri]],
  "DAB" => ["2026-05-18", "2026-05-20", [:method, :uri]],
  "CBI" => [nil, nil, [:method, :host]],
  "BAM" => ["2026-03-25", "2026-03-27", [:method, :host, :path]],
  "BCBO" => ["2007-12-27", "2007-12-28", [:method, :uri]],
  "BI" => ["2026-03-01", "2026-03-05", [:method, :host]],
  "BCU" => ["2026-03-27", "2026-03-31", [:method, :host]],
  "BM" => ["2026-09-02", "2026-09-04", [:method, :uri]],
  "CBVS" => ["2026-09-07", "2026-09-08", [:method, :uri]],
  "NRB" => ["2026-04-01", "2026-04-05", [:method, :host]],
}.freeze

output = ARGV.fetch(0, "tmp/rate-parity/components.jsonl")
live = ENV.fetch("LIVE_PROVIDERS", "").split(",")
cases = if live.empty?
          CASES
        else
          CASES.slice(*live).transform_values { |_, _, matching| [nil, nil, matching] }
            .merge("BOZ" => ["2006-01-01", Date.today.to_s, [:method, :uri]])
            .slice(*live)
        end
FileUtils.mkdir_p(File.dirname(output))
failures = []
File.open(output, "w") do |file|
  cases.each do |key, (after, upto, match_requests_on)|
    adapter = Provider::Adapters.const_get(key).new
    fetch = lambda do
      adapter.fetch(after: after && Date.parse(after), upto: upto && Date.parse(upto))
    end
    rows = if live.empty?
             VCR.use_cassette(key.downcase, match_requests_on:, &fetch)
           else
             VCR.turned_off do
               WebMock.allow_net_connect!
               fetch.call
             end
           end
    rows.each { |row| file.puts(JSON.generate(row.merge(provider: key))) }
    puts "#{key}: #{rows.size} rows"
    $stdout.flush
  rescue StandardError => e
    failures << key
    warn "#{key}: #{e.class}: #{e.message.lines.first}"
  end
end

connection = SQLite3::Database.new(":memory:")
RateComponents.register(connection)
connection.execute(<<~SQL)
  CREATE TABLE observations (
    provider TEXT, date TEXT, base TEXT, quote TEXT, mid REAL, bid REAL, ask REAL, old REAL
  )
SQL
connection.transaction do
  statement = connection.prepare("INSERT INTO observations VALUES (?, ?, ?, ?, ?, ?, ?, ?)")
  File.foreach(output) do |line|
    row = JSON.parse(line, symbolize_names: true)
    mid = row.key?(:mid) ? row[:mid] : row[:rate]
    statement.execute(row[:provider], row[:date], row[:base], row[:quote], RatePrecision.normalize(mid),
                      row[:bid], row[:ask], RatePrecision.normalize(row[:rate]),)
  end
  statement.close
end
connection.results_as_hash = true
expressions = {
  plain: "COALESCE(mid, (bid + ask) / 2.0)",
  normalized: <<~SQL,
    COALESCE(mid, CASE WHEN bid IS NOT NULL AND ask IS NOT NULL
      THEN CAST(printf('%.12g', (bid + ask) / 2.0) AS REAL) END)
  SQL
  decimal: <<~SQL,
    COALESCE(mid, CASE WHEN provider = 'BOJA' AND bid = 0 THEN frankfurter_midpoint(ask, ask)
      ELSE frankfurter_midpoint(bid, ask) END)
  SQL
}
expressions.each do |name, expression|
  summary = connection.execute(<<~SQL)
    SELECT provider, COUNT(*) AS rows, SUM(old IS NOT (#{expression})) AS differences
    FROM observations GROUP BY provider
  SQL
  puts "#{name}: #{JSON.generate(summary)}"
  examples = connection.execute(<<~SQL)
    SELECT *, #{expression} AS calculated FROM observations WHERE old IS NOT (#{expression}) LIMIT 10
  SQL
  puts "#{name} examples: #{JSON.generate(examples)}" unless examples.empty?
  abort "Decimal parity failed" if name == :decimal && examples.any?
end
abort "Missing providers: #{failures.join(", ")}" unless failures.empty?
