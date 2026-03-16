# frozen_string_literal: true

require "sequel"

env = ENV.fetch("APP_ENV", "development")
Sequel.connect("sqlite://#{Dir.pwd}/db/frankfurter_#{env}.sqlite3")
