# frozen_string_literal: true

require_relative "../../helper"
require "versions/v2"
require "rack/test"

describe "Precision regression" do
  include Rack::Test::Methods

  def app
    Versions::V2
  end

  # Returns the effective decimal places of a float: the number of decimal
  # places the value would have when formatted at the heuristic precision.
  # Trailing zeros are significant here — if round(value, dp) equals value,
  # the value has at least dp effective decimal places.
  def effective_dp(roundable, value)
    heuristic_dp = roundable.roundable_dp(value)
    # Count dp from serialized form (JSON drops trailing zeros)
    serialized_dp = value.to_s.split(".").last&.length || 0
    # If the value naturally terminates before heuristic_dp (trailing zeros),
    # serialized_dp < heuristic_dp is acceptable — the value is exact.
    # Check this by seeing if re-rounding at heuristic_dp changes the value.
    formatted = format("%.#{heuristic_dp}f", value)
    value_at_heuristic = formatted.to_f
    # If value equals its heuristic-rounded form, trailing zeros are the cause
    (value - value_at_heuristic).abs < Float::EPSILON ? heuristic_dp : serialized_dp
  end

  it "does not reduce decimal places for any currency" do
    roundable = Object.new.extend(Roundable)

    get "/rates"
    assert_predicate last_response, :ok?

    rates = JSON.parse(last_response.body)
    rates.each do |record|
      value = record["rate"]
      output_dp = effective_dp(roundable, value)
      heuristic_dp = roundable.roundable_dp(value)
      _(output_dp).must_be :>=, heuristic_dp,
        "#{record["quote"]} rate #{value} has #{output_dp} effective dp, expected >= #{heuristic_dp}"
    end
  end

  it "single-provider query preserves at least heuristic precision" do
    roundable = Object.new.extend(Roundable)

    get "/rates?providers=ecb"
    assert_predicate last_response, :ok?

    rates = JSON.parse(last_response.body)
    rates.each do |record|
      value = record["rate"]
      output_dp = effective_dp(roundable, value)
      heuristic_dp = roundable.roundable_dp(value)
      _(output_dp).must_be :>=, heuristic_dp,
        "#{record["quote"]} rate #{value} has #{output_dp} effective dp, expected >= #{heuristic_dp}"
    end
  end
end
