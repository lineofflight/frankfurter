# frozen_string_literal: true

require_relative "helper"
require "cache"

describe Cache do
  it "does nothing when not configured" do
    cache = Cache.new(zone_id: nil, api_token: nil)

    _(cache.purge).must_be_nil
  end
end
