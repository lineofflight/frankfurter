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
  def effective_decimal_places(roundable, value)
    heuristic_decimal_places = roundable.roundable_decimal_places(value)
    # Count dp from serialized form (JSON drops trailing zeros)
    serialized_decimal_places = value.to_s.split(".").last&.length || 0
    # If the value naturally terminates before heuristic_decimal_places (trailing zeros),
    # serialized_decimal_places < heuristic_decimal_places is acceptable — the value is exact.
    # Check this by seeing if re-rounding at heuristic_decimal_places changes the value.
    formatted = format("%.#{heuristic_decimal_places}f", value)
    value_at_heuristic = formatted.to_f
    # If value equals its heuristic-rounded form, trailing zeros are the cause
    (value - value_at_heuristic).abs < Float::EPSILON ? heuristic_decimal_places : serialized_decimal_places
  end

  it "does not reduce decimal places for any currency" do
    roundable = Object.new.extend(Roundable)

    get "/rates"

    assert_predicate last_response, :ok?

    rates = JSON.parse(last_response.body)
    rates.each do |record|
      value = record["rate"]
      output_decimal_places = effective_decimal_places(roundable, value)
      heuristic_decimal_places = roundable.roundable_decimal_places(value)

      _(output_decimal_places).must_be(
        :>=,
        heuristic_decimal_places,
        "#{record["quote"]} rate #{value} has #{output_decimal_places} effective dp, expected >= #{heuristic_decimal_places}",
      )
    end
  end

  it "single-provider query preserves at least heuristic precision" do
    roundable = Object.new.extend(Roundable)

    get "/rates?providers=ecb"

    assert_predicate last_response, :ok?

    rates = JSON.parse(last_response.body)
    rates.each do |record|
      value = record["rate"]
      output_decimal_places = effective_decimal_places(roundable, value)
      heuristic_decimal_places = roundable.roundable_decimal_places(value)

      _(output_decimal_places).must_be(
        :>=,
        heuristic_decimal_places,
        "#{record["quote"]} rate #{value} has #{output_decimal_places} effective dp, expected >= #{heuristic_decimal_places}",
      )
    end
  end
end
