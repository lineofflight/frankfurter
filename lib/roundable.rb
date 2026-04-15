# frozen_string_literal: true

# To paraphrase Wikipedia, most currency pairs are quoted to four decimal
# places. An exception to this is exchange rates with a value of less than
# 1.000, which are quoted to five or six decimal places. Exchange rates
# greater than around 20 are usually quoted to three decimal places and
# exchange rates greater than 80 are quoted to two decimal places.
# Currencies over 5000 are usually quoted with no decimal places.
#
# https://en.wikipedia.org/wiki/Exchange_rate#Quotations
module Roundable
  def round(value, precision: nil)
    decimal_places = roundable_decimal_places(value)
    decimal_places = [decimal_places, precision].max if precision
    Float(format("%<value>.#{decimal_places}f", value:))
  end

  def roundable_decimal_places(value)
    if value > 5000
      0
    elsif value > 80
      2
    elsif value > 20
      3
    elsif value > 1
      4
    elsif value > 0.0001
      5
    else
      6
    end
  end
end
