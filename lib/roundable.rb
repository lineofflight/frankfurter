# frozen_string_literal: true

# To paraphrase Wikipedia, most currency pairs are quoted to four decimal places. An exception to this is exchange rates
# with a value of less than 1.000, which are quoted to five or six decimal places. Exchange rates greater than around 20
# are usually quoted to three decimal places and exchange rates greater than 80 are quoted to two decimal places.
# Currencies over 5000 are usually quoted with no decimal places.
#
# Below 1, fixed decimals run out of digits: IRR to USD, at 0.00000054, would round to 0.000001. Those keep five
# significant figures instead, which for values from 0.1 to 1 is the same five decimal places.
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
    else
      Float(format("%<value>.5g", value:))
    end
  end
end
