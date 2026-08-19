# frozen_string_literal: true

require "sequel"

# Ingest-precision policy: strip binary float noise from a rate before it is stored.
#
# Adapters routinely synthesize figures no source published: buy/sell midpoints, per-unit rescaling, cross rates. Every
# one of those is float arithmetic, and float arithmetic leaves noise in the low digits.
#
#   (181.5264 + 181.76) / 2.0  => 181.64319999999998
#   744.92 / 100.0             => 7.449199999999999
#
# The noise is invisible on blended shapes, which round on the way out (Roundable), but single-provider responses echo
# the stored digits verbatim so the provider's own published precision survives (RateQuery#emit_records). There the
# noise reaches the API, and no serialization policy can repair it once it is in the table.
#
# So round on the way in. No reference rate is published to more than about ten significant digits, and a double carries
# fifteen to seventeen, which leaves a wide band between the deepest real digit and the noise floor. Twelve sits in that
# band: above every digit a provider actually publishes, below every digit arithmetic invents.
#
# Rollups and blends are excluded on purpose. They are time and cross-provider averages, so their trailing digits are
# synthetic by construction, and both paths round on output anyway.
module RatePrecision
  DIGITS = 12

  FORMAT = "%.#{DIGITS}g".freeze

  class << self
    def normalize(rate)
      return rate unless rate.is_a?(Float) && rate.finite?

      Float(format(FORMAT, rate))
    end

    # The same rounding SQLite-side, for stripping noise from rows stored before this policy existed.
    def sql(column)
      Sequel.cast(Sequel.function(:printf, FORMAT, column), Float)
    end
  end
end
