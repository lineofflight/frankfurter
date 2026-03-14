# frozen_string_literal: true

require_relative "../boot"

require "minitest/autorun"
require "minitest/around/spec"
require "minitest/focus"
require "vcr"
require "webmock/minitest"

VCR.configure do |c|
  c.cassette_library_dir = "spec/vcr_cassettes"
  c.hook_into(:webmock)
end
