# frozen_string_literal: true

require_relative "helper"
require "providers"

describe Providers do
  it "auto-discovers providers" do
    _(Providers.all).wont_be_empty
  end

  it "returns only enabled providers" do
    seeded_keys = Provider.map(&:key)

    Providers.enabled.each do |provider|
      _(seeded_keys).must_include(provider.key)
    end
  end
end
