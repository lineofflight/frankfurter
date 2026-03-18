# frozen_string_literal: true

require "sequel"

url = ENV.fetch("DATABASE_URL") do
  env = ENV["APP_ENV"]
  db_name = env ? "frankfurter_#{env}" : "frankfurter"
  "sqlite://#{Dir.pwd}/db/#{db_name}.sqlite3"
end
Sequel.connect(url)
