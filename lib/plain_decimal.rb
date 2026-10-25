# frozen_string_literal: true

require "bigdecimal"
require "oj"

# Oj and Float#to_s write values under 0.0001 in exponent notation (5.4341e-07). That is valid JSON, but readers, people
# and agents alike, take in 0.00000054341 at a glance, and rate APIs such as Wise write it that way. Only negative
# exponents occur: values large enough for a positive one round to integers.
module PlainDecimal
  # A number in a value position. Strings never hold one, as nothing we serialize puts a digit right after a delimiter.
  EXPONENT = /(?<=[:,\[])-?\d+\.\d+e-\d+/

  class << self
    def dump(object)
      json = Oj.dump(object, mode: :compat)
      json.include?("e-") ? json.gsub(EXPONENT) { |number| expand(number) } : json
    end

    def format(value)
      string = value.to_s
      string.include?("e-") ? expand(string) : string
    end

    private

    def expand(number)
      BigDecimal(number).to_s("F")
    end
  end
end
