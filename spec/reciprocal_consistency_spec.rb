# frozen_string_literal: true

require_relative "helper"
require "rack/test"
require "versions/v2"

# Contract tests for the V2 API:
#   - reciprocal: rate(A,B) * rate(B,A) == 1 (rounded)
#   - cross-rate transitivity: rate(A,B) * rate(B,C) * rate(C,A) == 1 (rounded)
#   - pegged-base anchor: pegged base requests resolve to the peg, not provider noise
#
# The reciprocal and transitivity tests fail on `main` for non-USD bases — they're the
# defining checks of the fix. The pegged-base test passes today; it's a regression guard.
describe "V2 reciprocal consistency" do
  include Rack::Test::Methods

  let(:app) { Versions::V2.freeze }

  def rate_for(base:, quote:)
    get("/rates?base=#{base}&quotes=#{quote}")
    raise "request failed: #{last_response.status}" unless last_response.ok?

    row = Oj.load(last_response.body).find { |r| r["quote"] == quote }
    raise "no rate returned for #{base}->#{quote}" unless row

    row["rate"]
  end

  it "rate(A,B) * rate(B,A) rounds to 1.0 for USD/GBP" do
    forward = rate_for(base: "USD", quote: "GBP")
    backward = rate_for(base: "GBP", quote: "USD")

    _((forward * backward).round(4)).must_equal(1.0)
  end

  it "rate(A,B) * rate(B,A) rounds to 1.0 for USD/CAD" do
    forward = rate_for(base: "USD", quote: "CAD")
    backward = rate_for(base: "CAD", quote: "USD")

    _((forward * backward).round(4)).must_equal(1.0)
  end

  it "transitivity holds across USD, EUR, GBP" do
    a_b = rate_for(base: "USD", quote: "EUR")
    b_c = rate_for(base: "EUR", quote: "GBP")
    c_a = rate_for(base: "GBP", quote: "USD")

    _((a_b * b_c * c_a).round(4)).must_equal(1.0)
  end

  it "pegged base resolves to the peg, not provider noise" do
    rate = rate_for(base: "AED", quote: "USD")

    _(rate).must_equal((1.0 / 3.6725).round(5))
  end
end
