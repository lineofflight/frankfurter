# frozen_string_literal: true

require "logger"

$LOAD_PATH << File.expand_path("lib", __dir__)

LOGGER = Logger.new($stdout)
