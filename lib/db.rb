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

Sequel.connect(url)
