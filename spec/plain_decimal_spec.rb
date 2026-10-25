# frozen_string_literal: true

require_relative "helper"
require "plain_decimal"

describe PlainDecimal do
  it "writes small values as plain decimals" do
    json = PlainDecimal.dump([{ rate: 5.4341e-07 }, { rate: 1.0e-06 }, 5.4341e-07])

    _(json).must_equal('[{"rate":0.00000054341},{"rate":0.000001},0.00000054341]')
  end

  it "leaves other values as Oj writes them" do
    object = { rate: 1.1218, amount: 25807, date: "2026-10-23", name: "Iranian Rial" }

    _(PlainDecimal.dump(object)).must_equal(Oj.dump(object, mode: :compat))
  end

  it "reads back as the same number" do
    _(Oj.load(PlainDecimal.dump({ rate: 1.1854e-05 }))["rate"]).must_equal(1.1854e-05)
  end

  it "formats single values" do
    _(PlainDecimal.format(3.8749e-05)).must_equal("0.000038749")
    _(PlainDecimal.format(0.0027267)).must_equal("0.0027267")
    _(PlainDecimal.format(25807)).must_equal("25807")
  end
end
