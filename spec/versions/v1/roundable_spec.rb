# frozen_string_literal: true

require_relative "../../helper"
require "roundable"

describe Roundable do
  include Roundable

  it "rounds values over 5,000 to zero decimal places" do
    _(round(5000.123456)).must_equal(5000)
  end

  it "rounds values over 80 and below 5,000 to two decimal places" do
    _(round(80.123456)).must_equal(80.12)
    _(round(4999.123456)).must_equal(4999.12)
  end

  it "rounds values over 20 and below 80 to three decimal places" do
    _(round(79.123456)).must_equal(79.123)
    _(round(20.123456)).must_equal(20.123)
  end

  it "rounds values over 1 and below 20 to four decimal places" do
    _(round(19.123456)).must_equal(19.1235)
    _(round(1.123456)).must_equal(1.1235)
  end

  it "rounds values below 1 to five significant figures" do
    _(round(0.123456)).must_equal(0.12346)
    _(round(0.0123456)).must_equal(0.012346)
    _(round(0.0000655)).must_equal(0.0000655)
  end

  it "keeps the digits of values far below 1" do
    _(round(1 / 1_840_216.0)).must_equal(0.00000054341)
    _(round(1 / 84_362.0)).must_equal(0.000011854)
  end
end
