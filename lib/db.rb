# frozen_string_literal: true

require "sequel"

env = ENV["APP_ENV"]
db_name = env ? "frankfurter_#{env}" : "frankfurter"
Sequel.connect("sqlite://#{Dir.pwd}/db/#{db_name}.sqlite3")
