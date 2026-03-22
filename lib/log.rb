# frozen_string_literal: true

require "logger"

module Log
  class << self
    def logger
      @logger ||= ::Logger.new($stdout).tap do |l|
        l.level = ::Logger::WARN if ENV["APP_ENV"] == "test"
      end
    end

    def info(message) = logger.info(message)
  end
end
