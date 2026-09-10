# frozen_string_literal: true

require_relative "../boot"
require_relative "fixtures"

Fixtures.seed!

require "minitest/autorun"
require "minitest/mock"
require "minitest/around/spec"
require "minitest/focus"
require "vcr"
require "webmock"
require "parallel_tests/test/runtime_logger" if ENV.key?("TEST_ENV_NUMBER")

VCR.configure do |c|
  c.cassette_library_dir = "spec/vcr_cassettes"
  c.hook_into(:webmock)
  c.filter_sensitive_data("<TCMB_API_KEY>") { ENV["TCMB_API_KEY"] } if ENV["TCMB_API_KEY"]
  c.filter_sensitive_data("<FRED_API_KEY>") { ENV["FRED_API_KEY"] } if ENV["FRED_API_KEY"]
  c.filter_sensitive_data("<BAM_API_KEY>") { ENV["BAM_API_KEY"] } if ENV["BAM_API_KEY"]
  c.filter_sensitive_data("<BANXICO_API_KEY>") { ENV["BANXICO_API_KEY"] } if ENV["BANXICO_API_KEY"]
  c.filter_sensitive_data("<BCCH_USER>") { ENV["BCCH_USER"] } if ENV["BCCH_USER"]
  c.filter_sensitive_data("<BCCH_PASS>") { ENV["BCCH_PASS"] } if ENV["BCCH_PASS"]
  c.filter_sensitive_data("<BOT_API_KEY>") { ENV["BOT_API_KEY"] } if ENV["BOT_API_KEY"]
end

module Minitest
  class Spec
    around do |test|
      Sequel::Model.db.transaction do
        test.call
        raise Sequel::Rollback
      end
    end
  end
end
