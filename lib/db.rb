# frozen_string_literal: true

require "sequel"

url = ENV.fetch("DATABASE_URL") do
  env = ENV["APP_ENV"]
  db_name = env ? "frankfurter_#{env}" : "frankfurter"
  "sqlite://#{Dir.pwd}/db/#{db_name}.sqlite3"
end

unless url.start_with?("sqlite")
  abort "Frankfurter now uses SQLite. Remove DATABASE_URL or set it to a sqlite URL."
end

busy_timeout_ms = Integer(ENV.fetch("SQLITE_BUSY_TIMEOUT", 60_000))
max_connections = Integer(ENV.fetch("DB_MAX_CONNECTIONS", 8))
connect_sqls = [
  "PRAGMA journal_mode=WAL",
  "PRAGMA synchronous=NORMAL",
  "PRAGMA mmap_size=134217728",
  "PRAGMA journal_size_limit=27103364",
].freeze

DB = Sequel.connect(
  url,
  after_connect: proc { |conn| conn.busy_handler_timeout = busy_timeout_ms },
  connect_sqls:,
  max_connections:,
)
