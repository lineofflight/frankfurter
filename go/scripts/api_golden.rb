# frozen_string_literal: true

# Replays the API corpus through the Ruby app (Rack::MockRequest against App, full middleware stack) on the spec
# fixture plus edge rows, and writes the database and every response as gzipped JSON for the Go parity test in
# internal/api (TestGoldenAPI).
#
#   DATABASE_URL=sqlite://<scratch>/api.sqlite3 APP_ENV=test \
#     mise exec -- bundle exec ruby -Ilib -r./boot go/scripts/api_golden.rb 2026-09-29 \
#     go/internal/api/testdata/corpus.txt go/internal/api/testdata/golden/api.json.gz
#
# The date pins Date.today, so the fixture and every request see the same day whenever the file is regenerated. The
# database must be a throwaway file: the script migrates it and replaces its contents. The corpus format and its date
# tokens are described at the top of corpus.txt.

require "date"
require "json"
require "zlib"

abort "api_golden.rb: run with APP_ENV=test" unless ENV["APP_ENV"] == "test"
url = ENV.fetch("DATABASE_URL", "")
abort "api_golden.rb: point DATABASE_URL at a throwaway sqlite file" unless url.start_with?("sqlite://")
abort "usage: api_golden.rb YYYY-MM-DD CORPUS OUT.json.gz" unless ARGV.size == 3

TODAY = Date.parse(ARGV[0])
Date.singleton_class.define_method(:today) { TODAY }

# Migrate in a child process: older data migrations load Provider before the frequency column exists, and a Provider
# class defined then would never exclude non-blending providers in this process.
migrate = 'require "db"; Sequel.extension(:migration); Sequel::Migrator.run(DB, "db/migrate")'
system(RbConfig.ruby, "-Ilib", "-r./boot", "-e", migrate, exception: true)

require "db"
require "rack/mock"
require_relative "../../spec/fixtures"
require "app"

Fixtures.seed!

def insert(provider, date, base, quote, mid: nil, bid: nil, ask: nil)
  Rate.dataset.insert(provider:, date:, base:, quote:, mid:, bid:, ask:)
end

# Edge rows. Changing them changes every response over the affected dates; regenerate the golden file after.
#
# An ECB series seen on one day only: snapshots within 14 days carry it at an older date than the rest.
insert("ECB", Fixtures.business_day(200), "EUR", "AUD", mid: 1.65)
# A holiday: ECB publishes nothing on one business day, so single dates carry forward and ranges snap back.
Rate.where(provider: "ECB", date: Fixtures.business_day(100)).delete
# An unknown code and an expired one on the latest day: raw v1 quotes include them, the v1 catalogue does not.
insert("ECB", Fixtures.latest_date, "EUR", "ZZZ", mid: 2.0)
insert("ECB", Fixtures.latest_date, "EUR", "SLL", mid: 2.0)
Fixtures.send(:rebuild_rollups!)
Fixtures.send(:rebuild_currencies!)

TOKENS = {
  "today" => ->(_) { TODAY },
  "tomorrow" => ->(_) { TODAY + 1 },
  "latest" => ->(_) { Fixtures.latest_date },
  "sunday" => ->(_) { Fixtures.recent_sunday },
  "bday" => ->(n) { Fixtures.business_day(n) },
  "ago" => ->(n) { Fixtures.latest_date - n },
}.freeze

def expand(text)
  text.gsub(/\{(\w+)(?::(\d+))?\}/) do
    fn = TOKENS.fetch(Regexp.last_match(1)) { abort "api_golden.rb: unknown token #{Regexp.last_match(0)}" }
    fn.call(Regexp.last_match(2).to_i).to_s
  end
end

def dump_tables
  tables = DB.tables - [:schema_info]
  tables.sort.to_h do |table|
    columns = DB[table].columns - (table == :rates ? [:rate] : [])
    rows = DB[table].select(*columns).order(*columns).map do |row|
      columns.map { |c| row[c].is_a?(Date) ? row[c].to_s : row[c] }
    end
    [table, { columns:, rows: }]
  end
end

app = App.freeze.app
responses = File.readlines(ARGV[1], chomp: true).filter_map do |line|
  next if line.strip.empty? || line.start_with?("#")

  request, *header_lines = line.split(" | ")
  method, target = request.split(" ", 2)
  path = expand(target)
  headers = header_lines.to_h { |h| h.split(": ", 2) }
  # The query string goes in raw, so malformed escapes reach the app as a client would send them.
  bare, query = path.split("?", 2)
  env = Rack::MockRequest.env_for(bare, method:)
  env["QUERY_STRING"] = query.to_s
  headers.each { |name, value| env["HTTP_#{name.upcase.tr("-", "_")}"] = value }

  status, response_headers, body = app.call(env)
  chunks = []
  body.each { |chunk| chunks << chunk }
  body.close if body.respond_to?(:close)
  text = chunks.join

  entry = {
    request: line,
    method:,
    path:,
    headers:,
    status:,
    response_headers: response_headers.to_h { |k, v| [k.downcase, Array(v).join(", ")] },
  }
  if text.empty?
    entry[:empty] = true
  elsif response_headers["content-type"].to_s.include?("json")
    entry[:json] = JSON.parse(text)
  elsif text.dup.force_encoding("UTF-8").valid_encoding?
    entry[:text] = text
  else
    require "digest"
    entry[:sha256] = Digest::SHA256.hexdigest(text)
  end
  entry
end

out = { today: TODAY.to_s, tables: dump_tables, responses: }
Zlib::GzipWriter.open(ARGV[2]) { |gz| gz.write(JSON.generate(out)) }
warn "api_golden.rb: #{responses.size} responses"
