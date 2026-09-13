# frozen_string_literal: true

require "rate_precision"
require "bigdecimal"

# The adapter's rate remains an ingest-validation and compatibility value. Only source components are written; the
# database resolves rate when it is selected. Adapters without components continue to supply their reference as rate.
module RateComponents
  class << self
    def midpoint(bid, ask)
      return unless bid && ask

      ((BigDecimal(bid.to_s) + BigDecimal(ask.to_s)) / 2).to_f
    end

    def attributes(record)
      mid = record.key?(:mid) ? record[:mid] : record[:rate]
      record.except(:rate).merge(mid: RatePrecision.normalize(mid), bid: record[:bid], ask: record[:ask])
    end
  end
end
