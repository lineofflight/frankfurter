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
  def round(value)
    if value > 5000
      value.round
    elsif value > 80
      Float(format("%<value>.2f", value:))
    elsif value > 20
      Float(format("%<value>.3f", value:))
    elsif value > 1
      Float(format("%<value>.4f", value:))
    elsif value > 0.0001
      Float(format("%<value>.5f", value:))
    else
      Float(format("%<value>.6f", value:))
    end
  end
end
