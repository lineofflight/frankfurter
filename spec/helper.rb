# frozen_string_literal: true

require_relative "../boot"

require "minitest/autorun"
require "minitest/mock"
require "minitest/around/spec"
require "minitest/focus"
require "vcr"
require "webmock"

VCR.configure do |c|
  c.cassette_library_dir = "spec/vcr_cassettes"
  c.hook_into(:webmock)
  c.filter_sensitive_data("<TCMB_API_KEY>") { ENV["TCMB_API_KEY"] } if ENV["TCMB_API_KEY"]
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
