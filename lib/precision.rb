# frozen_string_literal: true

module Precision
  class << self
    def significant_digits(value)
      str = value.to_s
      str = str.sub(/^-/, "")       # ignore sign
      str = str.sub(/\.0$/, "")     # strip trailing .0 for integer-like floats
      str = str.sub(/^0+\./, ".")   # strip leading zeros before decimal
      str = str.delete(".")         # remove decimal point
      str = str.sub(/^0+/, "")      # strip leading zeros (from 0.00xxx)
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
