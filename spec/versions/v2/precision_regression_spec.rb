# frozen_string_literal: true

require_relative "../../helper"
require "versions/v2"
require "rack/test"

describe "Precision regression" do
  include Rack::Test::Methods
  include Roundable

  def app
    Versions::V2
  end

  # Accounts for JSON dropping trailing zeros: if re-rounding at the heuristic
  # precision doesn't change the value, the missing digits were just zeros.
  def effective_decimal_places(value)
    heuristic = roundable_decimal_places(value)
    serialized = value.to_s.split(".").last&.length || 0
    value_at_heuristic = format("%.#{heuristic}f", value).to_f

    (value - value_at_heuristic).abs < Float::EPSILON ? heuristic : serialized
  end

  def assert_precision_preserved(path)
    get(path)

    assert_predicate(last_response, :ok?)

    JSON.parse(last_response.body).each do |record|
      value = record["rate"]
      effective = effective_decimal_places(value)
      heuristic = roundable_decimal_places(value)

      _(effective).must_be(
        :>=,
        heuristic,
        "#{record["quote"]} rate #{value} has #{effective} effective dp, expected >= #{heuristic}",
      )
    end
  end

  it "does not reduce decimal places for any currency" do
    assert_precision_preserved("/rates")
  end

  it "single-provider query preserves at least heuristic precision" do
    assert_precision_preserved("/rates?providers=ecb")
  end
end
