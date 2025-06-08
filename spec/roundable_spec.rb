# frozen_string_literal: true

require_relative "helper"
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

  it "rounds values below 1 to five decimal places" do
    _(round(0.123456)).must_equal(0.12346)
  end

  it "rounds values below 0.0001 to six decimal places" do
    _(round(0.0000655)).must_equal(0.000066)
  end

  it "conforms to ECB conventions" do
    skip "We don't conform ¯_(ツ)_/¯"
    require "day"
    rates = Day.all.sample.rates.to_a

    rates.shuffle.each_value do |rate|
      _(round(rate)).must_equal(rate)
    end
  end
end
