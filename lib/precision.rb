# frozen_string_literal: true

require "bigdecimal"

module Precision
  class << self
    def significant_digits(value)
      str = BigDecimal(value.to_s).to_s("F")
      str = str.sub(/^-/, "")
      str = str.sub(/\.0$/, "")
      str = str.sub(/^0+\./, ".")
      str = str.delete(".")
      str = str.sub(/^0+/, "")
      str.length
    end

    def derive(rates)
      rates.group_by { |r| r[:quote] }.transform_values do |group|
        digits = group.map { |r| significant_digits(r[:rate]) }.sort
        digits[digits.size / 2]
      end
    end

    def decimal_places(sig_digits, value)
      [sig_digits - Math.log10(value.abs).floor - 1, 0].max
    end
  end
end
