# frozen_string_literal: true

require "logger"

module Log
  class << self
    def logger
      @logger ||= ::Logger.new($stdout).tap do |l|
        l.level = ::Logger::ERROR if ENV["APP_ENV"] == "test"
      end
    end

    def debug(message) = logger.debug(message)
    def info(message) = logger.info(message)
    def warn(message) = logger.warn(message)
    def error(message) = logger.error(message)
  end
end
